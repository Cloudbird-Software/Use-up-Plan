package main

import (
	"fmt"
	"strings"
	"testing"
)

// mod 是被测模块路径的简写。
const mod = modulePath

func TestCheckPackagesMainLocation(t *testing.T) {
	cases := []struct {
		name string
		pkgs []packageInfo
		want int // 期望的 GO-3 违规数
	}{
		{"cmd 入口通过", []packageInfo{{ImportPath: mod + "/cmd/use-up-plan", Name: "main"}}, 0},
		{"tools 入口通过", []packageInfo{{ImportPath: mod + "/tools/archlint", Name: "main"}}, 0},
		{"internal 下 main 违规", []packageInfo{{ImportPath: mod + "/internal/qdl", Name: "main"}}, 1},
		{"模块根 main 违规", []packageInfo{{ImportPath: mod, Name: "main"}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := checkPackages(c.pkgs, mod, map[string]bool{})
			got := countRule(errs, "GO-3")
			if got != c.want {
				t.Fatalf("GO-3 违规数 = %d, want %d, errs = %v", got, c.want, errs)
			}
		})
	}
}

func TestCheckPackagesRegistration(t *testing.T) {
	pkgs := []packageInfo{{ImportPath: mod + "/internal/qdl", Name: "qdl"}}
	if errs := checkPackages(pkgs, mod, map[string]bool{}); countRule(errs, "MOD-5") != 1 {
		t.Fatalf("未登记 internal 模块应触发 MOD-5, errs = %v", errs)
	}
	if errs := checkPackages(pkgs, mod, map[string]bool{"qdl": true}); len(errs) != 0 {
		t.Fatalf("已登记应通过, errs = %v", errs)
	}
	// 无 Go 代码的目录（go list 不会输出）天然不触发——裸 internal 包同理。
	if errs := checkPackages([]packageInfo{{ImportPath: mod + "/internal", Name: "internal"}}, mod, map[string]bool{}); len(errs) != 0 {
		t.Fatalf("裸 internal 包不应触发 MOD-5, errs = %v", errs)
	}
}

func TestCheckPackagesDeepImport(t *testing.T) {
	rules := map[string]bool{"qdl": true, "route": true}
	cases := []struct {
		name string
		pkgs []packageInfo
		want int // 期望的 MOD-1 违规数
	}{
		{"import 对方根包通过", []packageInfo{
			{ImportPath: mod + "/internal/route", Name: "route", Imports: []string{mod + "/internal/qdl"}},
		}, 0},
		{"import 自己的子包通过", []packageInfo{
			{ImportPath: mod + "/internal/qdl", Name: "qdl", Imports: []string{mod + "/internal/qdl/loader"}},
		}, 0},
		{"跨模块深入子包违规", []packageInfo{
			{ImportPath: mod + "/internal/route", Name: "route", Imports: []string{mod + "/internal/qdl/loader"}},
		}, 1},
		{"cmd 深入子包违规", []packageInfo{
			{ImportPath: mod + "/cmd/use-up-plan", Name: "main", Imports: []string{mod + "/internal/qdl/loader"}},
		}, 1},
		{"外部依赖不受规则约束", []packageInfo{
			{ImportPath: mod + "/internal/route", Name: "route", Imports: []string{"example.com/x/internal/a/b"}},
		}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := checkPackages(c.pkgs, mod, rules)
			got := countRule(errs, "MOD-1")
			if got != c.want {
				t.Fatalf("MOD-1 违规数 = %d, want %d, errs = %v", got, c.want, errs)
			}
		})
	}
}

func TestInternalModuleDeepImport(t *testing.T) {
	cases := []struct {
		path    string
		wantM   string
		wantDep bool
	}{
		{mod + "/internal/qdl", "qdl", false},
		{mod + "/internal/qdl/loader", "qdl", true},
		{mod + "/internal", "", false},
		{mod + "/internal/", "", false},
		{mod + "/internal//x", "", false},
		{mod + "/cmd/use-up-plan", "", false},
		{mod, "", false},
		{"example.com/other/internal/a/b", "", false},
	}
	for _, c := range cases {
		if m, deep := internalModuleDeepImport(c.path, mod); m != c.wantM || deep != c.wantDep {
			t.Errorf("internalModuleDeepImport(%q) = (%q,%v), want (%q,%v)", c.path, m, deep, c.wantM, c.wantDep)
		}
	}
}

func Example_deepImportPaths() {
	m, deep := internalModuleDeepImport(mod+"/internal/qdl/loader", mod)
	fmt.Println(m, deep)
	// Output: qdl true
}

// FuzzInternalModuleDeepImport 是 T-04 的 fuzz 种子（解析器代码必须带）：
// 任意输入不得 panic，且 deep ⇒ 可从返回的模块名反推路径前缀。
func FuzzInternalModuleDeepImport(f *testing.F) {
	f.Add(mod + "/internal/qdl/loader")
	f.Add(mod + "/internal/qdl")
	f.Add("example.com/x/internal/a/b")
	f.Add("")
	f.Add(mod + "/internal//")
	f.Fuzz(func(t *testing.T, path string) {
		m, deep := internalModuleDeepImport(path, mod)
		if m == "" {
			if deep {
				t.Fatalf("deep=true 但模块名为空: %q", path)
			}
			return
		}
		root := mod + "/internal/" + m
		if !strings.HasPrefix(path, root) {
			t.Fatalf("模块名 %q 与路径 %q 不一致", m, path)
		}
		if deep && !strings.HasPrefix(path, root+"/") {
			t.Fatalf("deep=true 但路径不是子包: %q", path)
		}
	})
}

func countRule(errs []string, rule string) int {
	n := 0
	for _, e := range errs {
		if strings.HasPrefix(e, rule+":") {
			n++
		}
	}
	return n
}
