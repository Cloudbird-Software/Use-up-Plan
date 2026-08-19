package qdl

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration 是 YAML 友好的时长类型，同时接受两种写法：
//
//	ISO 8601：PT5H / P7D / P1DT12H（Intent.md 种子 plan 采用，便于手写）
//	Go 原生：5h / 168h0m0s（time.ParseDuration 全集）
//
// 以 'P' 开头按 ISO 8601 解析（年/月按 365/30 天名义换算，仅描述性用途）。
type Duration struct{ time.Duration }

// MarshalYAML 输出 Go 原生时长串（确定性）。
func (d Duration) MarshalYAML() (interface{}, error) { return d.Duration.String(), nil }

// UnmarshalYAML 实现 goccy 接口：字符串 → Duration。
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("qdl: 时长必须是字符串: %w", err)
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// ParseDuration 解析 ISO 8601（P 前缀）或 Go 原生时长。
func ParseDuration(s string) (time.Duration, error) {
	if len(s) > 0 && (s[0] == 'P' || s[0] == 'p') {
		return parseISODuration(s)
	}
	return time.ParseDuration(s)
}

// parseISODuration 解析 P[nY][nM][nW][nD][T[nH][nM][nS]]。单位大小写不敏感；
// 日期单位（Y/M/W/D）不得出现在 T 之后，时间单位（H/M/S）不得出现在 T 之前；
// 至少要有一个「数值+单位」分量（P / PT 单独出现报错）。
func parseISODuration(s string) (time.Duration, error) {
	body := strings.ToLower(s[1:])
	inTime := false
	nParts := 0
	total := time.Duration(0)
	i := 0
	for i < len(body) {
		if body[i] == 't' && !inTime {
			inTime = true
			i++
			continue
		}
		j := i
		for j < len(body) && (body[j] >= '0' && body[j] <= '9' || body[j] == '.') {
			j++
		}
		if j == i || j >= len(body) {
			return 0, fmt.Errorf("qdl: 非法 ISO 8601 时长 %q", s)
		}
		n, err := strconv.ParseFloat(body[i:j], 64)
		if err != nil {
			return 0, fmt.Errorf("qdl: 非法 ISO 8601 时长 %q: %w", s, err)
		}
		switch unit := body[j]; unit {
		case 'y':
			if inTime {
				return 0, fmt.Errorf("qdl: 日期单位 Y 不得出现在 T 之后: %q", s)
			}
			total += time.Duration(n * 365 * 24 * float64(time.Hour))
		case 'm': // 日期部分=月（名义 30 天），时间部分=分钟
			if inTime {
				total += time.Duration(n * float64(time.Minute))
			} else {
				total += time.Duration(n * 30 * 24 * float64(time.Hour))
			}
		case 'w':
			if inTime {
				return 0, fmt.Errorf("qdl: 日期单位 W 不得出现在 T 之后: %q", s)
			}
			total += time.Duration(n * 7 * 24 * float64(time.Hour))
		case 'd':
			if inTime {
				return 0, fmt.Errorf("qdl: 日期单位 D 不得出现在 T 之后: %q", s)
			}
			total += time.Duration(n * 24 * float64(time.Hour))
		case 'h', 's':
			if !inTime {
				return 0, fmt.Errorf("qdl: 时间单位 %q 不得出现在 T 之前: %q", strings.ToUpper(string(unit)), s)
			}
			if unit == 'h' {
				total += time.Duration(n * float64(time.Hour))
			} else {
				total += time.Duration(n * float64(time.Second))
			}
		default:
			return 0, fmt.Errorf("qdl: 非法 ISO 8601 时长单位 %q in %q", string(unit), s)
		}
		nParts++
		i = j + 1
	}
	if nParts == 0 {
		return 0, fmt.Errorf("qdl: ISO 8601 时长缺少分量: %q", s)
	}
	if total < 0 {
		return 0, fmt.Errorf("qdl: 时长为负 %q", s)
	}
	return total, nil
}
