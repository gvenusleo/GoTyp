package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gotyp/internal/vault"
)

// markdownImages 为 MyGo 的后台加载器固定笔记目录，避免读取已切换的界面状态。
func markdownImages(ctx context.Context, dir string) func(string) (ui.ImageSource, error) {
	local := markdown.FileImages(dir)
	return func(dest string) (ui.ImageSource, error) {
		u, err := url.Parse(dest)
		if err != nil {
			return nil, err
		}
		switch u.Scheme {
		case "http", "https":
			return readNetworkImage(ctx, u)
		case "file":
			if u.Host != "" && !strings.EqualFold(u.Host, "localhost") || !filepath.IsAbs(u.Path) {
				return nil, errors.New("图片文件地址须为本机绝对路径")
			}
		case "":
			if u.Host != "" || u.Path == "" {
				return nil, errors.New("图片地址缺少本地路径或 HTTP(S) 协议")
			}
		default:
			return nil, fmt.Errorf("不支持的图片地址协议：%s", u.Scheme)
		}
		return local(u.Path)
	}
}

var markdownImageClient = &http.Client{Timeout: 10 * time.Second}

func readNetworkImage(ctx context.Context, u *url.URL) (ui.ImageSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := markdownImageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片失败，HTTP 状态码 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, vault.MaxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > vault.MaxImageBytes {
		return nil, vault.ErrTooLarge
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if kind == "image/svg+xml" || strings.EqualFold(path.Ext(resp.Request.URL.Path), ".svg") {
		svg, err := ui.ParseSVG(data)
		if err != nil {
			return nil, fmt.Errorf("无法解析 SVG：%w", err)
		}
		return svg, nil
	}
	img, _, _, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	return ui.NewBitmap(img), nil
}
