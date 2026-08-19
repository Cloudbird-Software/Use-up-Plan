package probe

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"sort"

	"github.com/goccy/go-yaml"
)

//go:embed playbooks/*.yaml
var playbookFS embed.FS

// Builtins 返回内置剧本库（playbooks/ 目录 embed 进二进制），按 ID 排序
// ——确定性遍历是复现与测试的硬要求。
func Builtins() ([]*Playbook, error) {
	entries, err := playbookFS.ReadDir("playbooks")
	if err != nil {
		return nil, fmt.Errorf("probe: 读取内置剧本目录: %w", err)
	}
	var out []*Playbook
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := playbookFS.ReadFile("playbooks/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("probe: 读取剧本 %s: %w", e.Name(), err)
		}
		pb, err := parsePlaybook(raw)
		if err != nil {
			return nil, fmt.Errorf("probe: 内置剧本 %s: %w", e.Name(), err)
		}
		out = append(out, pb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Load 从文件加载单个剧本。
func Load(path string) (*Playbook, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("probe: 读取剧本 %s: %w", path, err)
	}
	pb, err := parsePlaybook(raw)
	if err != nil {
		return nil, fmt.Errorf("probe: %s: %w", path, err)
	}
	return pb, nil
}

// parsePlaybook 严格解码 + 校验（未知字段报错——剧本 schema 的演进必须
// 显式，静默忽略新字段会让旧二进制跑出语义漂移的判定）。
func parsePlaybook(raw []byte) (*Playbook, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw), yaml.Strict())
	var pb Playbook
	if err := dec.Decode(&pb); err != nil {
		return nil, fmt.Errorf("解析剧本: %w", err)
	}
	if err := pb.Validate(); err != nil {
		return nil, err
	}
	return &pb, nil
}
