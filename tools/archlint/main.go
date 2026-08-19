// Command archlint 是 GOVERNANCE GO-3 / MOD-1 / MOD-5 的 Go 版架构边界门禁：
//
//	GO-3  main 包只允许出现在 cmd/ 与 tools/ 下
//	MOD-1 跨模块只能 import 对方根包（禁止深入 internal/<模块>/... 的子包）
//	MOD-5 internal/<模块> 落地时必须在本文件 internalModuleRules 登记边界规则
//
// 由 `make arch` 调用（进 CI check 门）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// modulePath 必须与 go.mod 的 module 声明保持一致。
const modulePath = "github.com/Cloudbird-Software/Use-up-Plan"

// internalModuleRules 是 internal 模块边界规则登记表（MOD-5：模块落地 PR 必须同步登记，
// 未登记即 `make arch` 失败）。key = internal/ 下第一级目录名；登记即表示该模块存在且边界可审计。
var internalModuleRules = map[string]bool{
	// Phase 0 落地 internal/qdl 时在此登记：
	// "qdl": true,
}

// packageInfo 是 `go list -json ./...` 输出的字段子集。
type packageInfo struct {
	ImportPath string   `json:"ImportPath"`
	Name       string   `json:"Name"`
	Imports    []string `json:"Imports"`
}

func main() {
	pkgs, err := listPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "archlint: go list 失败: %v\n", err)
		os.Exit(1)
	}
	errs := checkPackages(pkgs, modulePath, internalModuleRules)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "archlint: %s\n", e)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}

// listPackages 调用 go list -json ./... 并解码全部包对象。
func listPackages() ([]packageInfo, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return decodePackages(&stdout)
}

// decodePackages 解码 go list 输出的连续 JSON 对象流。
func decodePackages(r io.Reader) ([]packageInfo, error) {
	var pkgs []packageInfo
	dec := json.NewDecoder(r)
	for {
		var p packageInfo
		if err := dec.Decode(&p); err == io.EOF {
			return pkgs, nil
		} else if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
}

// checkPackages 对包图执行三条边界规则，返回违规描述（空切片 = 通过）。
func checkPackages(pkgs []packageInfo, module string, rules map[string]bool) []string {
	var errs []string
	for _, p := range pkgs {
		if p.Name == "main" && !allowedMainLocation(p.ImportPath, module) {
			errs = append(errs, fmt.Sprintf("GO-3: main 包 %s 不在 %s/cmd/ 或 %s/tools/ 下", p.ImportPath, module, module))
		}
		if m := internalModuleName(p.ImportPath, module); m != "" && !rules[m] {
			errs = append(errs, fmt.Sprintf("MOD-5: internal/%s 已含代码但未在 tools/archlint 的 internalModuleRules 登记边界规则", m))
		}
		srcMod := internalModuleName(p.ImportPath, module)
		for _, imp := range p.Imports {
			if dstMod, deep := internalModuleDeepImport(imp, module); deep && dstMod != srcMod {
				errs = append(errs, fmt.Sprintf("MOD-1: %s 深入 import %s —— 跨模块只能 import 对方根包", p.ImportPath, imp))
			}
		}
	}
	return errs
}

// allowedMainLocation 报告 main 包是否位于 cmd/ 或 tools/ 下。
func allowedMainLocation(importPath, module string) bool {
	return strings.HasPrefix(importPath, module+"/cmd/") ||
		strings.HasPrefix(importPath, module+"/tools/")
}

// internalModuleName 返回包所属的 internal 模块名；非 internal 模块包返回 ""。
func internalModuleName(importPath, module string) string {
	m, _ := internalModuleDeepImport(importPath, module)
	return m
}

// internalModuleDeepImport 返回（模块名, 是否深入子包）。
//
//	module/internal/qdl     -> ("qdl", false)  模块根包
//	module/internal/qdl/x   -> ("qdl", true)   模块子包
//	module/internal         -> ("", false)     裸 internal 包，不视为模块
//	其余（含模块外路径）      -> ("", false)
func internalModuleDeepImport(importPath, module string) (string, bool) {
	rest, ok := strings.CutPrefix(importPath, module+"/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] != "internal" || parts[1] == "" {
		return "", false
	}
	return parts[1], len(parts) > 2
}
