package ledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// mkCharge 构造合法 ChargeEvent 测试样本。
func mkCharge() *ChargeEvent {
	return &ChargeEvent{
		RequestID: "req-1",
		PlanID:    "anthropic/max20@2026-08",
		ChannelID: "claude_code_oauth",
		Model:     "claude-sonnet-4-6",
		Dims: map[qdl.Dim]float64{
			qdl.DimInputTokens:     12_000,
			qdl.DimOutputTokens:    3_000,
			qdl.DimCacheReadTokens: 40_000,
		},
		BucketDeltas: map[string]float64{"b_5h": 0.071, "b_7d_all": 0.071},
		ThetaVersion: "theta-2026-08-20T00:00:00Z",
	}
}

// TestEventJSONRoundTrip 六种事件的 JSON 往返等价（JSONL 存储契约）。
func TestEventJSONRoundTrip(t *testing.T) {
	reset := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	d := qdl.Distribution{Kind: qdl.DistLognormal, Params: map[string]float64{"mu": 2.7, "sigma": 1.0}}
	cases := []Payload{
		mkCharge(),
		&ObservationEvent{
			PlanID: "anthropic/max20@2026-08", BucketID: "b_5h",
			Semantic: qdl.SemUsedPct, RawValue: "93",
			Quantization: qdl.Quantization{Kind: "integer"},
			Source:       qdl.ObsResponseHeader, Trust: 0.9,
		},
		&WallHitEvent{
			PlanID: "anthropic/max20@2026-08", BucketID: "b_5h", RequestID: "req-9",
			ErrorBody: "rate limited", ResetHint: &reset,
			LedgerSnapshot: map[qdl.Dim]float64{qdl.DimInputTokens: 1_200_000, qdl.DimOutputTokens: 200_000},
		},
		&ResetObservedEvent{
			PlanID: "anthropic/max20@2026-08", BucketID: "b_5h",
			PrevU: 0.92, NewU: 0.0, ResetAtReported: &reset,
		},
		&ParamUpdateEvent{
			ParamID:         "anthropic.max20.C_5h",
			PosteriorBefore: &d,
			PosteriorAfter:  qdl.Distribution{Kind: qdl.DistLognormal, Params: map[string]float64{"mu": 2.75, "sigma": 0.8}},
			EvidenceIDs:     []int64{4, 7, 9},
			Reason:          "online",
		},
		&StructureUpdateEvent{
			PlanID: "zai/glm-coding-max@2026-08", BucketID: "b_5h_prompts", Field: "charge.granularity",
			PosteriorBefore: map[string]float64{"turn": 0.4, "request": 0.4, "step": 0.2},
			PosteriorAfter:  map[string]float64{"turn": 0.1, "request": 0.9},
		},
	}
	ts := time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC)
	for i, p := range cases {
		ev := Event{Seq: int64(i + 1), Ts: ts, Type: typeOf(p)}
		switch v := p.(type) {
		case *ChargeEvent:
			ev.Charge = v
		case *ObservationEvent:
			ev.Observation = v
		case *WallHitEvent:
			ev.WallHit = v
		case *ResetObservedEvent:
			ev.ResetObserved = v
		case *ParamUpdateEvent:
			ev.ParamUpdate = v
		case *StructureUpdateEvent:
			ev.StructureUpdate = v
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("[%d] marshal: %v", i, err)
		}
		var back Event
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("[%d] unmarshal: %v\n%s", i, err, b)
		}
		if err := back.Validate(); err != nil {
			t.Fatalf("[%d] 回读后校验: %v", i, err)
		}
		if back.Type != ev.Type || back.Seq != ev.Seq || !back.Ts.Equal(ev.Ts) {
			t.Fatalf("[%d] 信封不等: %+v vs %+v", i, ev, back)
		}
		b2, _ := json.Marshal(back)
		if string(b) != string(b2) {
			t.Fatalf("[%d] 二次序列化不稳定:\n%s\n%s", i, b, b2)
		}
	}
}

// TestPayloadValidate 每种事件的校验拒绝面（缺身份/负量/越界 trust/空账本/坏分布）。
func TestPayloadValidate(t *testing.T) {
	bad := []struct {
		name string
		p    Payload
	}{
		{"charge 缺 theta_version", &ChargeEvent{RequestID: "r", PlanID: "p"}},
		{"charge 负维度", &ChargeEvent{
			RequestID: "r", PlanID: "p", ThetaVersion: "t",
			Dims: map[qdl.Dim]float64{qdl.DimInputTokens: -1}}},
		{"observation 缺 bucket", &ObservationEvent{
			PlanID: "p", Semantic: qdl.SemUsedPct, RawValue: "1",
			Quantization: qdl.Quantization{Kind: "integer"}, Source: qdl.ObsResponseHeader, Trust: 1}},
		{"observation trust 越界", &ObservationEvent{
			PlanID: "p", BucketID: "b", Semantic: qdl.SemUsedPct, RawValue: "1",
			Quantization: qdl.Quantization{Kind: "integer"}, Source: qdl.ObsResponseHeader, Trust: 1.5}},
		{"wall_hit 空账本", &WallHitEvent{
			PlanID: "p", BucketID: "b", LedgerSnapshot: map[qdl.Dim]float64{}}},
		{"reset 缺 bucket", &ResetObservedEvent{PlanID: "p"}},
		{"param 缺 id", &ParamUpdateEvent{PosteriorAfter: qdl.Point(1)}},
		{"param 坏分布", &ParamUpdateEvent{
			ParamID: "x", PosteriorAfter: qdl.Distribution{Kind: qdl.DistNormal,
				Params: map[string]float64{"mu": 1}}}}, // 缺 sigma
		{"structure 概率和≠1", &StructureUpdateEvent{
			PlanID: "p", BucketID: "b", Field: "window.kind",
			PosteriorAfter: map[string]float64{"a": 0.5}}},
		{"structure 概率越界", &StructureUpdateEvent{
			PlanID: "p", BucketID: "b", Field: "window.kind",
			PosteriorAfter: map[string]float64{"a": 1.2, "b": -0.2}}},
	}
	for _, c := range bad {
		if err := c.p.Validate(); err == nil {
			t.Errorf("%s: 应被拒绝", c.name)
		}
	}
}

// TestEventValidateTypeMismatch 信封 type 与负载不匹配必须报错。
func TestEventValidateTypeMismatch(t *testing.T) {
	ev := Event{Seq: 1, Ts: time.Now(), Type: EventObservation, Charge: mkCharge()}
	if err := ev.Validate(); err == nil {
		t.Fatal("type/负载不匹配应报错")
	}
	ev = Event{Seq: 1, Ts: time.Now(), Type: EventCharge}
	if err := ev.Validate(); err == nil {
		t.Fatal("无负载应报错")
	}
}

// TestSanitize 凭证脱敏命中面：api key / bearer / jwt / aws / github / key=value。
//
// 凭证样例一律运行期拼接构造：源文件里出现完整凭证形态（jwt 三段、
// ghp_ 前缀 + 20+ 字符）会命中 gitleaks 的 generic-api-key / github-pat
// 规则，hygiene 门禁直接打回——测试数据也是数据，不豁免。
func TestSanitize(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxIn0." + "abc123-_"
	ghp := "ghp_" + "16C7e42F292c" + "6912E7710c838347Ae178B4a"
	cases := []struct{ in, wantSub string }{
		{`error: invalid sk-abc123defGHIjklMNOpqrstu`, "[REDACTED:api_key]"},
		{`Authorization: Bearer ` + jwt, "[REDACTED:bearer]"},
		{`token in body: ` + jwt, "[REDACTED:jwt]"},
		{`aws key AKIAIOSFODNN7EXAMPLE leaked`, "[REDACTED:aws_access_key]"},
		{`pat ` + ghp, "[REDACTED:github_pat]"},
		{`config: api_key="supersecretvalue123"`, "api_key=[REDACTED:secret]"},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); !strings.Contains(got, c.wantSub) {
			t.Errorf("Sanitize(%q) = %q，应含 %q", c.in, got, c.wantSub)
		}
	}
	// 正常错误文本不受影响
	plain := `{"error":{"type":"rate_limit_error","message":"5h window exhausted"}}`
	if got := Sanitize(plain); got != plain {
		t.Errorf("普通文本被误杀: %q", got)
	}
}
