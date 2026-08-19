package ledger

import "regexp"

// 脱敏（Intent §10 ops 风险表）：事件库的 error_body / response_headers 可能
// 含凭证与 PII，存储边界统一替换。规则保守：只命中高置信度的凭证形态，
// 宁可漏过可疑串（事后审计），不可误杀正常错误文本。
type redactor struct {
	name string
	re   *regexp.Regexp
	repl string // $1 = 需要保留的前缀（如 key 名）
}

var redactors = []*redactor{
	{name: "api_key", re: regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), repl: "[REDACTED:api_key]"},
	{name: "bearer", re: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`), repl: "[REDACTED:bearer]"},
	{name: "jwt", re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`), repl: "[REDACTED:jwt]"},
	{name: "aws_access_key", re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), repl: "[REDACTED:aws_access_key]"},
	{name: "github_pat", re: regexp.MustCompile(`(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`), repl: "[REDACTED:github_pat]"},
	{name: "query_secret", re: regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|secret|password)["']?\s*[:=]\s*["']?([A-Za-z0-9._~+/=-]{8,})`), repl: "$1=[REDACTED:secret]"},
}

// Sanitize 对文本做凭证脱敏：按规则逐个替换，规则间独立（先命中先替换，
// 已替换产物不会再被后续规则二次展开）。
func Sanitize(s string) string {
	for _, r := range redactors {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
