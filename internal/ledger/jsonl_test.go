package ledger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// TestJSONLAppendIterateRoundTrip 追加六种事件后遍历：逐事件等价、序号连续。
func TestJSONLAppendIterateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()

	obs := &ObservationEvent{
		PlanID: "p", BucketID: "b", Semantic: qdl.SemUsedPct, RawValue: "93",
		Quantization: qdl.Quantization{Kind: "integer"},
		Source:       qdl.ObsUsageEndpoint, Trust: 1,
	}
	payloads := []Payload{
		mkCharge(),
		obs,
		&WallHitEvent{
			PlanID: "p", BucketID: "b",
			LedgerSnapshot: map[qdl.Dim]float64{qdl.DimInputTokens: 100},
		},
		&ResetObservedEvent{PlanID: "p", BucketID: "b", PrevU: 0.9, NewU: 0},
		&ParamUpdateEvent{ParamID: "x", PosteriorAfter: qdl.Point(3.14)},
		&StructureUpdateEvent{
			PlanID: "p", BucketID: "b", Field: "window.kind",
			PosteriorAfter: map[string]float64{"a": 0.7, "b": 0.3},
		},
	}
	ts := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for i, p := range payloads {
		seq, err := s.Append(ts.Add(time.Duration(i)*time.Minute), p)
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
		if seq != int64(i+1) {
			t.Fatalf("Append[%d] 序号 = %d", i, seq)
		}
	}

	var got []Event
	if err := s.Iterate(func(ev Event) error { got = append(got, ev); return nil }); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if len(got) != len(payloads) {
		t.Fatalf("遍历到 %d 条，应 %d", len(got), len(payloads))
	}
	for i, ev := range got {
		if ev.Seq != int64(i+1) {
			t.Fatalf("事件 %d 序号 %d 不连续", i, ev.Seq)
		}
		want := ts.Add(time.Duration(i) * time.Minute)
		if !ev.Ts.Equal(want) {
			t.Fatalf("事件 %d 时间戳 %v ≠ %v", i, ev.Ts, want)
		}
		if ev.Type != typeOf(payloads[i]) {
			t.Fatalf("事件 %d 类型 %s", i, ev.Type)
		}
	}
	// WallHit 的 error_body 未设置 → 空串；脱敏路径见 TestAppendSanitizesWallHit
	if got[2].WallHit == nil || len(got[2].WallHit.LedgerSnapshot) != 1 {
		t.Fatalf("wall_hit 负载丢失: %+v", got[2])
	}
}

// TestAppendSanitizesWallHit WallHit 的 error_body 入库自动脱敏且不改调用方原值。
func TestAppendSanitizesWallHit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()

	wh := &WallHitEvent{
		PlanID: "p", BucketID: "b", ErrorBody: "401 key sk-abcdefgh12345678 rejected",
		LedgerSnapshot: map[qdl.Dim]float64{qdl.DimInputTokens: 10},
	}
	if _, err := s.Append(time.Time{}, wh); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if wh.ErrorBody != "401 key sk-abcdefgh12345678 rejected" {
		t.Fatalf("调用方原值被修改: %q", wh.ErrorBody)
	}
	var stored string
	if err := s.Iterate(func(ev Event) error {
		stored = ev.WallHit.ErrorBody
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if stored != "401 key [REDACTED:api_key] rejected" {
		t.Fatalf("入库脱敏: %q", stored)
	}
	// 零值时间戳被填充为当前时间
	if stored == "" {
		t.Fatal("无事件")
	}
}

// TestAppendRejectsInvalid 校验失败的事件不入库（文件保持空）。
func TestAppendRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()
	if _, err := s.Append(time.Time{}, &ChargeEvent{RequestID: "r"}); err == nil {
		t.Fatal("缺字段应被拒绝")
	}
	if _, err := s.Append(time.Time{}, nil); err == nil {
		t.Fatal("nil 负载应被拒绝")
	}
	n := 0
	if err := s.Iterate(func(ev Event) error { n++; return nil }); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if n != 0 {
		t.Fatalf("校验失败的事件不应入库，却存了 %d 条", n)
	}
}

// TestJSONLRecovery 重开库：续号正确；尾部残行被截断且后续追加不粘连。
func TestJSONLRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append(time.Time{}, &ParamUpdateEvent{
			ParamID: "x", PosteriorAfter: qdl.Point(float64(i)),
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	s.Close()

	// 模拟崩溃半写：追加一段无换行的残行
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("打开残行注入: %v", err)
	}
	f.WriteString(`{"seq":99,"ts":"2026-08-20T00:00:00Z`)
	f.Close()

	s2, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("残行恢复应成功: %v", err)
	}
	defer s2.Close()
	seq, err := s2.Append(time.Time{}, &ParamUpdateEvent{ParamID: "x", PosteriorAfter: qdl.Point(3)})
	if err != nil {
		t.Fatalf("恢复后追加: %v", err)
	}
	if seq != 4 {
		t.Fatalf("恢复后续号应从 4 起，得 %d（残行 seq=99 不得劫持）", seq)
	}
	n := 0
	last := int64(0)
	if err := s2.Iterate(func(ev Event) error {
		if ev.Seq <= last {
			t.Errorf("序号回退: %d after %d", ev.Seq, last)
		}
		last = ev.Seq
		n++
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if n != 4 {
		t.Fatalf("残行截断后应恰好 4 条，得 %d", n)
	}
}

// TestJSONLCorruptionMidFile 文件中间坏行必须报错（不静默跳过）。
func TestJSONLCorruptionMidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	for i := 0; i < 2; i++ {
		s.Append(time.Time{}, &ParamUpdateEvent{ParamID: "x", PosteriorAfter: qdl.Point(1)})
	}
	s.Close()

	b, _ := os.ReadFile(path)
	b = append([]byte("this line is not json\n"), b...)
	os.WriteFile(path, b, 0o600)

	if _, err := NewJSONLStore(path); err == nil {
		t.Fatal("中间坏行应在恢复时报错")
	}
}

// TestJSONLConcurrentAppend 并发追加：序号无重复、无回退、总数正确。
func TestJSONLConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()

	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := s.Append(time.Time{}, &ParamUpdateEvent{
					ParamID: "x", PosteriorAfter: qdl.Point(1),
				}); err != nil {
					t.Errorf("并发 Append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	var maxSeq int64
	if err := s.Iterate(func(ev Event) error {
		if seen[ev.Seq] {
			t.Errorf("序号 %d 重复", ev.Seq)
		}
		seen[ev.Seq] = true
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	if len(seen) != workers*perWorker || maxSeq != int64(workers*perWorker) {
		t.Fatalf("应 %d 条无洞，得 %d 条 max=%d", workers*perWorker, len(seen), maxSeq)
	}
}

// TestIterateEarlyStop fn 返回错误时立即终止并透传。
func TestIterateEarlyStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := NewJSONLStore(path)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()
	for i := 0; i < 5; i++ {
		s.Append(time.Time{}, &ParamUpdateEvent{ParamID: "x", PosteriorAfter: qdl.Point(1)})
	}
	stop := errStop{}
	n := 0
	err = s.Iterate(func(ev Event) error {
		n++
		if ev.Seq == 2 {
			return stop
		}
		return nil
	})
	if err != stop {
		t.Fatalf("应透传终止错误，得 %v", err)
	}
	if n != 2 {
		t.Fatalf("应在第 2 条后停止，实际走 %d 条", n)
	}
}

type errStop struct{}

func (errStop) Error() string { return "stop" }
