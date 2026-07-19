package security

import (
	"regexp"
	"strings"
)

// Rule 一条安全扫描规则（正则模式）。
type Rule struct {
	ID          string // RULE-SEC-xxx
	Category    string // secret/sast/prompt
	Severity    string // critical/high/medium/low
	Title       string
	Pattern     *regexp.Regexp
	Description string
	Remediation string
	Confidence  float64
}

// ruleSet 全部扫描规则。密钥泄露(RULE-SEC-001)为强制红线。
var ruleSet = []Rule{
	// ---- 密钥泄露（RULE-SEC-001，零容忍）----
	{ID: "RULE-SEC-001a", Category: "secret", Severity: "critical", Confidence: 0.95,
		Title: "AWS Access Key", Description: "检测到 AWS 访问密钥 ID（AKIA 开头）",
		Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		Remediation: "立即吊销该密钥，改用 Vault/环境变量注入，从代码与历史中清除"},
	{ID: "RULE-SEC-001b", Category: "secret", Severity: "critical", Confidence: 0.9,
		Title: "私钥(Private Key)", Description: "检测到 PEM 私钥块",
		Pattern:  regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
		Remediation: "私钥不得入库；移至密钥管理系统，轮换并清理历史"},
	{ID: "RULE-SEC-001c", Category: "secret", Severity: "high", Confidence: 0.8,
		Title: "硬编码凭据赋值", Description: "检测到 api_key/secret/token/password 的字符串赋值",
		Pattern:  regexp.MustCompile(`(?i)(?:api[_-]?key|secret|access[_-]?token|auth[_-]?token|password|passwd|pwd)\s*[:=]\s*["'][^"']{6,}["']`),
		Remediation: "凭据移至配置中心/环境变量，禁止硬编码"},
	{ID: "RULE-SEC-001d", Category: "secret", Severity: "high", Confidence: 0.75,
		Title: "Google API Key", Description: "检测到 Google API 密钥（AIza 开头）",
		Pattern:  regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
		Remediation: "限制密钥使用范围/轮换，移至密钥管理"},
	{ID: "RULE-SEC-001e", Category: "secret", Severity: "medium", Confidence: 0.6,
		Title: "JWT Token", Description: "检测到疑似 JWT",
		Pattern:  regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
		Remediation: "JWT 可能含敏感信息，确认是否泄露并必要时吊销"},
	// ---- SAST-lite（RULE-SEC-003 SQL 拼接等）----
	{ID: "RULE-SEC-003a", Category: "sast", Severity: "high", Confidence: 0.6,
		Title: "疑似 SQL 字符串拼接", Description: "SQL 语句中出现变量拼接（注入风险）",
		Pattern:  regexp.MustCompile(`($1i)["']($2:SELECT|INSERT|UPDATE|DELETE|DROP)\b.*["']\s*\+|\+\s*["'].*($3:WHERE|FROM|VALUES)`),
		Remediation: "使用参数化查询，禁止拼接 SQL"},
	{ID: "RULE-SEC-003b", Category: "sast", Severity: "medium", Confidence: 0.5,
		Title: "eval/exec 执行动态输入", Description: "eval/exec 处理疑似外部输入（代码注入风险）",
		Pattern:  regexp.MustCompile(`(?i)\b(?:eval|exec|Function)\s*\([^)]*(?:request|input|prompt|req\.)`),
		Remediation: "禁止 eval/exec 外部输入；改用安全解析器"},
	// ---- 提示注入 / 越狱（RULE-SEC-010）----
	{ID: "RULE-SEC-010a", Category: "prompt", Severity: "high", Confidence: 0.7,
		Title: "提示注入：覆盖指令", Description: "检测到「忽略上述/之前指令」类越权改写",
		Pattern:  regexp.MustCompile(`(?i)(?:ignore|disregard|forget)\s+(?:all\s+)?(?:the\s+)?(?:previous|prior|above|earlier)\s+(?:instructions?|prompts?|rules?)`),
		Remediation: "对该输入拒答或脱敏；加强系统提示边界与输出过滤"},
	{ID: "RULE-SEC-010b", Category: "prompt", Severity: "medium", Confidence: 0.6,
		Title: "提示注入：套取系统提示", Description: "检测到要求泄露系统提示/角色的企图",
		Pattern:  regexp.MustCompile(`(?i)(?:reveal|show|print|repeat|leak)\s+(?:your|the|any)?\s*(?:system\s+)?(?:prompt|instructions?|rules?|initial)\b`),
		Remediation: "拒绝泄露系统提示；记录审计"},
}

// Scanner 扫描器接口（默认 RegexScanner；后续可插 Semgrep/Trippy 等外部工具）。
type Scanner interface {
	Scan(content, scanType string) []Finding
}

// RegexScanner 默认 Go 正则扫描实现。
type RegexScanner struct{}

// Scan 按扫描类型匹配规则，返回未入库的 Finding（ID 空，由 Store 赋值）。
// scanType: secret/sast/prompt/full（full=全部）。
func (RegexScanner) Scan(content, scanType string) []Finding {
	var out []Finding
	for _, rule := range ruleSet {
		if scanType != "full" && rule.Category != scanType {
			continue
		}
		matches := rule.Pattern.FindAllStringIndex(content, -1)
		for _, m := range matches {
			out = append(out, Finding{
				Category:    rule.Category,
				RuleID:      rule.ID,
				Severity:    rule.Severity,
				Title:       rule.Title,
				Description: rule.Description,
				Remediation: rule.Remediation,
				Confidence:  rule.Confidence,
				LineNumber:  intPtr(lineOf(content, m[0])),
				CodeSnippet: snippet(content, m[0], m[1]),
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// lineOf 返回字节偏移对应的行号（1-based）。
func lineOf(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
}

// snippet 取命中行（去除首尾空白，截断到 120 字符）。
func snippet(content string, start, end int) string {
	lineStart := start
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := end
	for lineEnd < len(content) && content[lineEnd] != '\n' {
		lineEnd++
	}
	s := strings.TrimSpace(content[lineStart:lineEnd])
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func intPtr(i int) *int { return &i }

// maxSeverity 多条发现的聚合风险等级。
func maxSeverity(findings []Finding) string {
	rank := map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}
	cur := 0
	word := "clean"
	for _, f := range findings {
		if r, ok := rank[f.Severity]; ok && r > cur {
			cur, word = r, f.Severity
		}
	}
	return word
}
