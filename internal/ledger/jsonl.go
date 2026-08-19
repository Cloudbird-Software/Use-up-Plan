package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Store 是事件存储的深接口：追加 + 全量遍历，就这两个操作。
// append-only 意味着没有更新、没有删除——修正只能靠追加补偿事件（未来需要
// 时再定义），这是「一切状态可从事件流重建」的结构保证。
type Store interface {
	// Append 追加单个事件：存储边界统一做负载校验与脱敏，返回分配的序号
	//（严格递增、进程内连续）。ts 为零值时取当前 UTC 时间。
	Append(ts time.Time, p Payload) (int64, error)
	// Iterate 按序号升序遍历全部已落盘事件；fn 返回非 nil 时立即终止并
	// 原样透传该错误。遍历期间不建议并发追加（见 JSONLStore 注释）。
	Iterate(fn func(ev Event) error) error
	Close() error
}

// JSONLStore 是 Store 的文件实现（AR-7：事件→数据层 JSONL 起步，SQL 迁移
// 后置到 Phase 3+ 按数据层政策提案）。一行一个 JSON 信封，追加写 + fsync。
//
// 崩溃恢复语义：打开时扫描现有行恢复最大序号；尾部无换行的残行（崩溃中的
// 半次写）被截断丢弃——残行从未是已提交事件。文件中间的坏行则是数据损坏，
// 必须报错（静默跳过会让重放结果无声偏离真相）。
//
// 并发语义：Append 由互斥锁串行化，进程内安全（go test -race 强制）。
// Iterate 打开独立只读句柄、读到当前 EOF 为止，是一个时点遍历；遍历期间
// 另一 goroutine 的追加可能产生读到一半的行，此时遍历会在该行报错——
// 需要与追加并行的场景，应在追加间隙调用。
type JSONLStore struct {
	mu   sync.Mutex
	f    *os.File
	seq  int64 // 已分配的最大序号（重启后从现有文件恢复）
	path string
}

// NewJSONLStore 打开（或创建）path 上的 JSONL 事件库并完成崩溃恢复。
func NewJSONLStore(path string) (*JSONLStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ledger: 打开事件库 %s: %w", path, err)
	}
	maxSeq, cleanEnd, err := scanFile(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("ledger: 恢复 %s: %w", path, err)
	}
	if cleanEnd < fileSize(f) {
		// 尾部残行：截断到最后一个完整行末——半次写不是事件
		if err := f.Truncate(cleanEnd); err != nil {
			f.Close()
			return nil, fmt.Errorf("ledger: 截断 %s 残行: %w", path, err)
		}
	}
	if _, err := f.Seek(cleanEnd, io.SeekStart); err != nil {
		f.Close()
		return nil, fmt.Errorf("ledger: 定位 %s 末尾: %w", path, err)
	}
	return &JSONLStore{f: f, seq: maxSeq, path: path}, nil
}

// Append 实现 Store.Append。
func (s *JSONLStore) Append(ts time.Time, p Payload) (int64, error) {
	if p == nil {
		return 0, fmt.Errorf("ledger: Append(nil)")
	}
	if err := p.Validate(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts.IsZero() {
		ts = time.Now()
	}
	s.seq++
	ev := Event{Seq: s.seq, Ts: ts.UTC(), Type: typeOf(p)}
	switch v := p.(type) {
	case *ChargeEvent:
		ev.Charge = v
	case *ObservationEvent:
		ev.Observation = v
	case *WallHitEvent:
		cp := *v // 拷贝后脱敏：不改调用方的原值，存储层无外溢副作用
		cp.ErrorBody = Sanitize(v.ErrorBody)
		ev.WallHit = &cp
	case *ResetObservedEvent:
		ev.ResetObserved = v
	case *ParamUpdateEvent:
		ev.ParamUpdate = v
	case *StructureUpdateEvent:
		ev.StructureUpdate = v
	}
	b, err := json.Marshal(ev)
	if err != nil {
		s.seq--
		return 0, fmt.Errorf("ledger: 序列化事件: %w", err)
	}
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		s.seq--
		return 0, fmt.Errorf("ledger: 写事件 seq=%d: %w", ev.Seq, err)
	}
	if err := s.f.Sync(); err != nil {
		return 0, fmt.Errorf("ledger: fsync 事件 seq=%d: %w", ev.Seq, err)
	}
	return ev.Seq, nil
}

// Iterate 实现 Store.Iterate：独立只读句柄、按行解码、信封一致性校验。
func (s *JSONLStore) Iterate(fn func(ev Event) error) error {
	r, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 从未写过的库：空流
		}
		return fmt.Errorf("ledger: 读事件库 %s: %w", s.path, err)
	}
	defer r.Close()

	br := bufio.NewReaderSize(r, 64*1024)
	var lastSeq int64
	lineNum := 0
	for {
		line, rerr := br.ReadBytes('\n')
		complete := rerr == nil
		if len(bytes.TrimSpace(line)) > 0 {
			lineNum++
			if !complete {
				return nil // 尾部残行：半次写，忽略
			}
			var ev Event
			if err := json.Unmarshal(bytes.TrimSpace(line), &ev); err != nil {
				return fmt.Errorf("ledger: %s 第 %d 行损坏: %w", s.path, lineNum, err)
			}
			if err := ev.Validate(); err != nil {
				return fmt.Errorf("ledger: %s 第 %d 行: %w", s.path, lineNum, err)
			}
			if ev.Seq <= lastSeq {
				return fmt.Errorf("ledger: %s 第 %d 行序号 %d 未递增（上一行 %d）",
					s.path, lineNum, ev.Seq, lastSeq)
			}
			lastSeq = ev.Seq
			if err := fn(ev); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("ledger: 读 %s: %w", s.path, rerr)
		}
	}
}

// Close 关闭底层文件句柄。
func (s *JSONLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

// typeOf 返回负载对应的事件类型（与 Event.Validate 的分派保持一致）。
func typeOf(p Payload) EventType {
	switch p.(type) {
	case *ChargeEvent:
		return EventCharge
	case *ObservationEvent:
		return EventObservation
	case *WallHitEvent:
		return EventWallHit
	case *ResetObservedEvent:
		return EventResetObserved
	case *ParamUpdateEvent:
		return EventParamUpdate
	case *StructureUpdateEvent:
		return EventStructureUpdate
	}
	return ""
}

// scanFile 遍历文件的全部完整行，返回（最大序号, 最后一个完整行的末尾偏移）。
// 完整行必须可解析且信封合法，否则报数据损坏。
func scanFile(r io.Reader) (maxSeq int64, cleanEnd int64, err error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var lastSeq int64
	lineNum := 0
	for {
		line, rerr := br.ReadBytes('\n')
		complete := rerr == nil
		if len(bytes.TrimSpace(line)) > 0 {
			lineNum++
			if !complete {
				return lastSeq, cleanEnd, nil // 尾部残行：不算事件，cleanEnd 停在上一个完整行
			}
			var ev Event
			if uerr := json.Unmarshal(bytes.TrimSpace(line), &ev); uerr != nil {
				return 0, 0, fmt.Errorf("第 %d 行损坏: %w", lineNum, uerr)
			}
			if verr := ev.Validate(); verr != nil {
				return 0, 0, fmt.Errorf("第 %d 行: %w", lineNum, verr)
			}
			if ev.Seq <= lastSeq {
				return 0, 0, fmt.Errorf("第 %d 行序号 %d 未递增（上一行 %d）", lineNum, ev.Seq, lastSeq)
			}
			lastSeq = ev.Seq
			cleanEnd += int64(len(line))
		} else if complete {
			cleanEnd += int64(len(line)) // 空白行不是事件但占位，保持偏移前进
		}
		if rerr == io.EOF {
			return lastSeq, cleanEnd, nil
		}
		if rerr != nil {
			return 0, 0, rerr
		}
	}
}

// fileSize 返回当前读写位置处的文件大小（r 已位于起始）。
func fileSize(f *os.File) int64 {
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}
