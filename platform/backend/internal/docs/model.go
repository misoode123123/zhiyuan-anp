// Package docs 是「方案文档中心」——扫描 docs 目录，提供方案列表/内容/搜索。
package docs

// Doc 方案文档元数据。
type Doc struct {
	Path     string `json:"path"`     // 相对 docs_dir 的路径
	Title    string `json:"title"`    // H1 或首行
	Category string `json:"category"` // 一级目录
	Mtime    string `json:"mtime"`    // 修改时间
	Summary  string `json:"summary"`  // 正文摘要
}
