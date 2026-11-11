package tinymist

import (
	"context"
	"errors"
	"fmt"
)

// invertColors 反转版面而保留插图，照片和图表反转后会失真。
const invertColors = `{"rest":"always","image":"never"}`

// Preview 是一个运行中的预览任务。Done 在承载它的 tinymist 进程退出时关闭，任务随之失效。
type Preview struct {
	ID   string
	URL  string
	Done <-chan struct{}
}

// StartPreview 先把文档同步到 doc 的版本，再为它启动预览服务。服务只监听回环地址的随机端口，
// 渲染 tinymist 内存中的文本，随后续的 didChange 增量刷新。invert 为真时反转页面颜色。
func (cl *Client) StartPreview(ctx context.Context, doc Document, id string, invert bool) (Preview, error) {
	// 主实例的预览会把主文件钉在这篇笔记上，结束任务后也不解除，之后切换的笔记就收不到诊断，
	// 预览也退回磁盘内容。--not-primary 让每个任务用独立的编译实例，主实例只负责诊断和补全。
	args := []string{"--task-id", id, "--data-plane-host", "127.0.0.1:0", "--not-primary", "--no-open"}
	if invert {
		args = append(args, "--invert-colors", invertColors)
	}
	args = append(args, doc.Path)
	cl.mu.Lock()
	c, err := cl.syncLocked(doc)
	if err != nil {
		cl.mu.Unlock()
		return Preview{}, err
	}
	rid, ch, err := c.send("workspace/executeCommand", map[string]any{
		"command": "tinymist.doStartPreview", "arguments": []any{args},
	})
	cl.mu.Unlock()
	if err != nil {
		return Preview{}, err
	}
	var res struct {
		Port int `json:"staticServerPort"`
	}
	if err := c.wait(ctx, rid, ch, &res); err != nil {
		if ctx.Err() != nil {
			// 服务端可能已经启动了任务，没人接手就会一直占着端口。
			go killPreview(c, id)
		}
		return Preview{}, err
	}
	if res.Port <= 0 {
		go killPreview(c, id)
		return Preview{}, errors.New("tinymist 没有返回预览端口")
	}
	return Preview{ID: id, URL: fmt.Sprintf("http://127.0.0.1:%d/", res.Port), Done: c.done}, nil
}

// StopPreview 结束预览任务。进程已经退出时任务随之消失，什么也不做。
func (cl *Client) StopPreview(id string) {
	cl.mu.Lock()
	c := cl.conn
	cl.mu.Unlock()
	if c != nil && !c.closed() {
		killPreview(c, id)
	}
}

func killPreview(c *conn, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	c.call(ctx, "workspace/executeCommand", map[string]any{
		"command": "tinymist.doKillPreview", "arguments": []any{id},
	}, nil)
}
