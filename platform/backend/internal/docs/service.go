package docs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"zhiyuan-anp/platform/backend/internal/config"
)

// Service 扫描 docs 目录，提供列表/内容/搜索。
type Service struct {
	store *config.Store
}

// NewService 构造。store 用于读 docs_dir 配置（默认 ../../docs，相对 backend cwd）。
func NewService(store *config.Store) *Service { return &Service{store: store} }

func (s *Service) dir() string {
	return s.store.Get("docs_dir", "../../docs")
}

// List 扫描 docs_dir 下所有 .md，返回元数据列表。
func (s *Service) List() ([]Doc, error) {
	root := s.dir()
	var out []Doc
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		title, summary := readMeta(p)
		out = append(out, Doc{
			Path:     rel,
			Title:    title,
			Category: categoryOf(rel),
			Mtime:    info.ModTime().Format("2006-01-02 15:04"),
			Summary:  summary,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Content 读文档原文（防 path traversal）。
func (s *Service) Content(rel string) (string, error) {
	root, _ := filepath.Abs(s.dir())
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(clean, root) {
		return "", fmt.Errorf("非法路径")
	}
	b, err := os.ReadFile(clean)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Search 关键字匹配 title/summary/path（大小写不敏感）。
func (s *Service) Search(q string) ([]Doc, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return list, nil
	}
	var out []Doc
	for _, d := range list {
		if strings.Contains(strings.ToLower(d.Title+" "+d.Summary+" "+d.Path), q) {
			out = append(out, d)
		}
	}
	return out, nil
}

func categoryOf(rel string) string {
	parts := strings.SplitN(rel, "/", 2)
	if len(parts) < 2 {
		return "根"
	}
	return parts[0]
}

// readMeta 取 H1 作 title（无则首个非空非标题行），正文前几行去符号作 summary。
func readMeta(p string) (string, string) {
	f, err := os.Open(p)
	if err != nil {
		return filepath.Base(p), ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	title := ""
	var body []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ">") {
			continue
		}
		if title == "" {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				continue
			}
			title = line
		}
		body = append(body, line)
		if len(body) >= 6 {
			break
		}
	}
	if title == "" {
		title = filepath.Base(p)
	}
	summary := strings.Join(body, " ")
	if len([]rune(summary)) > 120 {
		summary = string([]rune(summary)[:120]) + "…"
	}
	return title, summary
}
