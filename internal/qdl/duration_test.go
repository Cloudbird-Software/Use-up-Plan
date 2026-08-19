package qdl

import (
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		// ISO 8601
		{"PT5H", 5 * time.Hour},
		{"P7D", 7 * 24 * time.Hour},
		{"P1DT12H", 36 * time.Hour},
		{"PT30M", 30 * time.Minute},
		{"P2W", 14 * 24 * time.Hour},
		{"PT90S", 90 * time.Second},
		{"P1Y", 365 * 24 * time.Hour}, // 名义换算
		{"P1M", 30 * 24 * time.Hour},  // 名义换算（非时间部分）
		{"PT1M", time.Minute},         // 时间部分
		{"p1dt2h", 26 * time.Hour},    // 小写容忍
		// Go 原生
		{"5h", 5 * time.Hour},
		{"168h0m0s", 168 * time.Hour},
		{"90s", 90 * time.Second},
		{"1.5h", 90 * time.Minute},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseDuration(%q) = (%v, %v), want (%v, nil)", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "P", "PT", "PX", "PT5X", "P1D2H", "-PT5H", "5x"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) 应报错", bad)
		}
	}
}

// Duration 作为结构体字段经 goccy 解码（ISO 8601 与 Go 原生两种写法）。
func TestDurationFieldDecode(t *testing.T) {
	type holder struct {
		ISO Duration `yaml:"iso"`
		Go  Duration `yaml:"go"`
	}
	var h holder
	src := []byte("iso: PT5H\ngo: 5h\n")
	if err := yaml.UnmarshalWithOptions(src, &h, yaml.Strict()); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if h.ISO.Duration != 5*time.Hour || h.Go.Duration != 5*time.Hour {
		t.Fatalf("两种写法应等价: iso=%v go=%v", h.ISO.Duration, h.Go.Duration)
	}
}

// Duration 序列化输出 Go 原生时长串，且可无损往返。
func TestDurationMarshalRoundTrip(t *testing.T) {
	type holder struct {
		Len Duration `yaml:"len"`
	}
	h := holder{Len: Duration{5 * time.Hour}}
	out, err := yaml.MarshalWithOptions(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "len: 5h0m0s\n" {
		t.Fatalf("规范输出应为 Go 原生时长串, got %q", out)
	}
	var back holder
	if err := yaml.UnmarshalWithOptions(out, &back, yaml.Strict()); err != nil {
		t.Fatalf("回读: %v", err)
	}
	if back.Len.Duration != h.Len.Duration {
		t.Fatalf("往返不一致: %v", back.Len.Duration)
	}
}
