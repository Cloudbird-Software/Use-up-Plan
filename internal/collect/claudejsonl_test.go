package collect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// claudeLine 构造一行真实形状的会话记录（字段名与 Claude Code JSONL 一致）。
func claudeLine(typ, ts, msgID, model string, in, cw, cr, out float64) string {
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	return `{"parentUuid":null,"isSidechain":false,"userType":"external","cwd":"/p","sessionId":"s1",` +
		`"version":"2.0.1","type":"` + typ + `","uuid":"u-` + msgID + `","timestamp":"` + ts + `",` +
		`"message":{"id":"` + msgID + `","type":"message","role":"assistant","model":"` + model + `",` +
		`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":` + n(in) + `,"cache_creation_input_tokens":` + n(cw) +
		`,"cache_read_input_tokens":` + n(cr) + `,"output_tokens":` + n(out) +
		`,"cache_creation":{"ephemeral_5m_input_tokens":0},"server_tool_use":{"web_search_requests":1}}}}`
}

func mustParseOne(t *testing.T, line string) []ClaudeTurn {
	t.Helper()
	turns, err := ParseClaudeJSONL(strings.NewReader(line + "\n"))
	if err != nil {
		t.Fatalf("ParseClaudeJSONL: %v", err)
	}
	return turns
}

// TestParseAssistantUsage 主路径：assistant 行四维 token 原样入账，
// 未知字段（server_tool_use 等）被忽略。
func TestParseAssistantUsage(t *testing.T) {
	turns := mustParseOne(t, claudeLine("assistant",
		"2026-08-19T10:00:00.123Z", "msg_01", "claude-sonnet-4-6", 4, 1837, 19223, 307))
	if len(turns) != 1 {
		t.Fatalf("应提取 1 条记录，得 %d", len(turns))
	}
	tu := turns[0]
	want := time.Date(2026, 8, 19, 10, 0, 0, 123000000, time.UTC)
	if !tu.Ts.Equal(want) {
		t.Fatalf("时间戳 %v ≠ %v", tu.Ts, want)
	}
	if tu.MsgID != "msg_01" || tu.Model != "claude-sonnet-4-6" {
		t.Fatalf("身份字段: %+v", tu)
	}
	for d, w := range map[qdl.Dim]float64{
		qdl.DimInputTokens: 4, qdl.DimCacheWriteTokens: 1837,
		qdl.DimCacheReadTokens: 19223, qdl.DimOutputTokens: 307,
	} {
		if tu.Dims[d] != w {
			t.Fatalf("维度 %s = %v，应 %v", d, tu.Dims[d], w)
		}
	}
	if len(tu.Dims) != 4 {
		t.Fatalf("应恰好 4 个 token 维，得 %d", len(tu.Dims))
	}
}

// TestParseSkipsNonAssistant user/system/summary 行、全零 usage 行与空行跳过
// ——事件流不被无计量信息的记录膨胀。
func TestParseSkipsNonAssistant(t *testing.T) {
	lines := []string{
		claudeLine("user", "2026-08-19T10:00:00Z", "m0", "x", 10, 0, 0, 5),
		`{"type":"summary","summary":"Session summary","leafUuid":"u1"}`,
		`{"type":"system","subtype":"init","sessionId":"s1"}`,
		"",
		claudeLine("assistant", "2026-08-19T10:00:01Z", "m1", "m", 0, 0, 0, 0), // 全零
		claudeLine("assistant", "2026-08-19T10:00:02Z", "m2", "m", 3, 0, 0, 7),
	}
	turns, err := ParseClaudeJSONL(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseClaudeJSONL: %v", err)
	}
	if len(turns) != 1 || turns[0].MsgID != "m2" {
		t.Fatalf("应只剩 1 条有效记录: %+v", turns)
	}
}

// TestParseSidechainKept sidechain（subagent）消息同样是真实计费请求，保留。
func TestParseSidechainKept(t *testing.T) {
	line := strings.Replace(claudeLine("assistant",
		"2026-08-19T10:00:00Z", "msg_s", "claude-haiku-4-5", 100, 0, 500, 20),
		`"isSidechain":false`, `"isSidechain":true`, 1)
	turns := mustParseOne(t, line)
	if len(turns) != 1 {
		t.Fatalf("sidechain 记录应保留，得 %d 条", len(turns))
	}
}

// TestParseBadJSONHardFails JSON 语法坏行是数据损坏：报错而非静默跳过
// （半份账本比没有账本更危险——会得出错误的外生消耗结论）。
func TestParseBadJSONHardFails(t *testing.T) {
	input := claudeLine("assistant", "2026-08-19T10:00:00Z", "m", "m", 1, 0, 0, 1) + "\n{broken\n"
	if _, err := ParseClaudeJSONL(strings.NewReader(input)); err == nil {
		t.Fatal("坏行应报错")
	}
}

// TestParseBadTimestampHardFails assistant 行缺时间戳或格式非法：报错
// （时间轴是窗口重放的地基，不可缺省）。
func TestParseBadTimestampHardFails(t *testing.T) {
	if _, err := ParseClaudeJSONL(strings.NewReader(
		`{"type":"assistant","message":{"id":"m","model":"m","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n")); err == nil {
		t.Fatal("缺时间戳应报错")
	}
}

// TestLoadClaudeLogsOrderAndMerge 目录遍历 + 跨文件时间排序。
func TestLoadClaudeLogsOrderAndMerge(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// b.jsonl 里的时间早于 a.jsonl：全局按时间排序后 b 的记录在前
	files := map[string]string{
		"a.jsonl": claudeLine("assistant", "2026-08-19T12:00:00Z", "late", "m", 1, 0, 0, 1) + "\n",
		"b.jsonl": claudeLine("assistant", "2026-08-19T09:00:00Z", "early", "m", 1, 0, 0, 1) + "\n" +
			claudeLine("user", "2026-08-19T09:30:00Z", "u", "m", 1, 0, 0, 1) + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(p, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	turns, err := LoadClaudeLogs(root)
	if err != nil {
		t.Fatalf("LoadClaudeLogs: %v", err)
	}
	if len(turns) != 2 || turns[0].MsgID != "early" || turns[1].MsgID != "late" {
		t.Fatalf("应按时间升序合并: %+v", turns)
	}
}

// TestLoadClaudeLogsEmptyDir 空目录是合法状态（无历史日志），不报错。
func TestLoadClaudeLogsEmptyDir(t *testing.T) {
	turns, err := LoadClaudeLogs(t.TempDir())
	if err != nil || len(turns) != 0 {
		t.Fatalf("空目录应返回空集: turns=%v err=%v", turns, err)
	}
}

// TestLoadClaudeLogsCorruptFileFails 一个文件损坏 → 整体报错（显式可见）。
func TestLoadClaudeLogsCorruptFileFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.jsonl"), []byte("{broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClaudeLogs(root); err == nil {
		t.Fatal("损坏文件应让 LoadClaudeLogs 报错")
	}
}

func FuzzParseClaudeJSONL(f *testing.F) {
	f.Add([]byte(claudeLine("assistant", "2026-08-19T10:00:00Z", "m", "m", 1, 2, 3, 4) + "\n"))
	f.Add([]byte(`{"type":"summary","summary":"x"}`))
	f.Add([]byte("\n\n"))
	f.Add([]byte(`{"type":"assistant","message":{"usage":{}}}`))
	f.Add([]byte(`{"type":"assistant","timestamp":"2026-08-19T10:00:00Z","message":{"usage":{"input_tokens":-1,"output_tokens":1e308}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		// 不变量：要么报错，要么产出的每条记录 dims 恰好四维且时间戳非零。
		turns, err := ParseClaudeJSONL(strings.NewReader(string(data)))
		if err != nil {
			return
		}
		for _, tu := range turns {
			if len(tu.Dims) != 4 {
				t.Fatalf("dims 维数 %d ≠ 4", len(tu.Dims))
			}
			if tu.Ts.IsZero() {
				t.Fatal("时间戳不应为零值")
			}
		}
	})
}
