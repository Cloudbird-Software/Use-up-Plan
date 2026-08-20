// Package collect 实现观测采集三通道（Intent §5.1：响应头/usage 字段为主、
// usage endpoint 为次、网页 DOM 为末）。本文件是本地日志通道的第一块：
// Claude Code 会话 JSONL 解析——精确 token 账本，ledger 校验的基准。
//
// 深接口边界：本包只做「原始日志 → 原始物理量」的解析，产出 ClaudeTurn
// （dims 为原始 token 数）。不计算扣减（那是 semantics 的事）、不写事件库
// （那是 audit/入账层的事）——分层保证解析器可独立 fuzz（T-04）。
package collect

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ClaudeTurn 是会话日志里一条可入账的 assistant 消息计量记录：一次真实
// API 请求的原始物理量。Dims 存真实 token 数（Intent §3.3：绝不存已加权
// 结果——重放的前提）。
type ClaudeTurn struct {
	Ts    time.Time
	MsgID string // message.id——ChargeEvent.RequestID 的来源
	Model string // 厂商模型 ID（model_multiplier 的 glob 匹配对象）
	Dims  map[qdl.Dim]float64
}

// claudeRecord 是 JSONL 一行的形状子集。未知字段整体忽略——Claude Code
// 格式随版本演进（新增 content 类型、usage 细分字段），解析器只认计量
// 必需的锚点，其余变化不应造成解析失败。
type claudeRecord struct {
	Type      string         `json:"type"` // assistant | user | system | summary | ...
	Timestamp string         `json:"timestamp"`
	Message   *claudeMessage `json:"message"`
}

type claudeMessage struct {
	ID    string       `json:"id"`
	Model string       `json:"model"`
	Usage *claudeUsage `json:"usage"`
}

// claudeUsage 是 Anthropic Messages API 的 usage 形状（token 维按
// Intent §1.3 分类学映射；server_tool_use 等多模态细分维暂不映射，
// 落地对应 plan 时再扩展）。
type claudeUsage struct {
	InputTokens              float64 `json:"input_tokens"`
	CacheCreationInputTokens float64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     float64 `json:"cache_read_input_tokens"`
	OutputTokens             float64 `json:"output_tokens"`
}

// ParseClaudeJSONL 解析单个 Claude Code 会话日志（一行一条 JSON 记录）。
// 只提取 type=assistant 且带 usage 的记录（含 sidechain——subagent 消息
// 同样是真实计费请求）；其余行（user/system/summary 等）跳过。
//
// JSON 语法坏行是数据损坏，报错（与 ledger.JSONLStore 同哲学：静默跳过
// 会让重放结果无声偏离真相）；usage 全零的 assistant 记录跳过——无计量
// 信息的空事件只会膨胀事件流。
func ParseClaudeJSONL(r io.Reader) ([]ClaudeTurn, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20) // 长消息行可达数 MB
	var turns []ClaudeTurn
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var rec claudeRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("collect: 第 %d 行不是合法 JSON: %w", line, err)
		}
		if rec.Type != "assistant" {
			continue
		}
		// 凡 assistant 行，时间戳即必填（不变量 2）——先验时间戳再判
		// message/usage，否则「缺 usage 且缺时间戳」的损坏行会被静默吞掉。
		ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("collect: 第 %d 行时间戳 %q 无法解析: %w", line, rec.Timestamp, err)
		}
		if rec.Message == nil || rec.Message.Usage == nil {
			continue
		}
		u := rec.Message.Usage
		// 负 token 是数据损坏：报错，绝不入账（半份/假账比没有账本更危险）。
		// 判零必须逐维进行——代数和会让 input=-1/output=1 相消成「无计量」。
		dims := map[qdl.Dim]float64{
			qdl.DimInputTokens:      u.InputTokens,
			qdl.DimCacheWriteTokens: u.CacheCreationInputTokens,
			qdl.DimCacheReadTokens:  u.CacheReadInputTokens,
			qdl.DimOutputTokens:     u.OutputTokens,
		}
		allZero := true
		for d, v := range dims {
			if v < 0 {
				return nil, fmt.Errorf("collect: 第 %d 行 %s 为负值（%v）——数据损坏", line, d, v)
			}
			if v != 0 {
				allZero = false
			}
		}
		if allZero {
			continue // 无计量信息
		}
		turns = append(turns, ClaudeTurn{
			Ts: ts.UTC(), MsgID: rec.Message.ID, Model: rec.Message.Model, Dims: dims,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("collect: 读取会话日志: %w", err)
	}
	return turns, nil
}

// LoadClaudeLogs 遍历 root（通常 ~/.claude/projects）下全部 *.jsonl 会话
// 文件，合并解析并按时间升序返回。同一请求只出现在一个会话文件里，
// 无需去重；跨文件时间交错由全局排序纠正。任何文件解析失败即报错——
// 半份账本比没有账本更危险（会得出错误的「外生消耗」结论）。
func LoadClaudeLogs(root string) ([]ClaudeTurn, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect: 遍历 %s: %w", root, err)
	}
	sort.Strings(files) // 同刻记录的次序稳定（确定性要求）
	var all []ClaudeTurn
	for _, f := range files {
		turns, err := parseClaudeFile(f)
		if err != nil {
			return nil, err
		}
		all = append(all, turns...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Ts.Before(all[j].Ts) })
	return all, nil
}

// DefaultClaudeProjectsDir 返回 Claude Code 会话日志根目录：
// $CLAUDE_PROJECTS_DIR 覆盖 > ~/.claude/projects（Intent §2.2 locator）。
func DefaultClaudeProjectsDir() (string, error) {
	if d := os.Getenv("CLAUDE_PROJECTS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("collect: 无法定位用户主目录: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func parseClaudeFile(path string) ([]ClaudeTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("collect: 打开会话日志 %s: %w", path, err)
	}
	defer f.Close()
	turns, err := ParseClaudeJSONL(f)
	if err != nil {
		return nil, fmt.Errorf("collect: %s: %w", path, err)
	}
	return turns, nil
}
