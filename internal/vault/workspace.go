package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// maxRecent 是最近仓库列表保留的数量。
const maxRecent = 8

// Workspace 是一个仓库的界面状态，不包含笔记正文。
type Workspace struct {
	Selected      string  `json:"selected,omitempty"`
	Mode          string  `json:"mode,omitempty"`
	SidebarWidth  float32 `json:"sidebarWidth,omitempty"`
	EditorPercent float32 `json:"editorPercent,omitempty"`
	// HideLineNumbers 记录用户关闭了行号，零值表示默认显示。
	HideLineNumbers bool `json:"hideLineNumbers,omitempty"`
	SidebarHidden   bool `json:"sidebarHidden,omitempty"`
}

// State 是保存在用户配置目录中的应用状态。它可以随时删除，不影响任何笔记。
type State struct {
	Recent     []string             `json:"recent,omitempty"`
	Workspaces map[string]Workspace `json:"workspaces,omitempty"`
}

// Touch 把仓库移到最近列表最前面，并丢弃超出上限的旧仓库及其界面状态。
func (s *State) Touch(root string) {
	s.Recent = slices.DeleteFunc(s.Recent, func(r string) bool { return r == root })
	s.Recent = slices.Insert(s.Recent, 0, root)
	if len(s.Recent) > maxRecent {
		for _, old := range s.Recent[maxRecent:] {
			delete(s.Workspaces, old)
		}
		s.Recent = s.Recent[:maxRecent]
	}
}

// SetWorkspace 记录一个仓库的界面状态。
func (s *State) SetWorkspace(root string, w Workspace) {
	if s.Workspaces == nil {
		s.Workspaces = map[string]Workspace{}
	}
	s.Workspaces[root] = w
}

// Clone 返回可以交给后台写入的独立副本。
func (s State) Clone() State {
	out := State{Recent: slices.Clone(s.Recent), Workspaces: make(map[string]Workspace, len(s.Workspaces))}
	for k, v := range s.Workspaces {
		out.Workspaces[k] = v
	}
	return out
}

// LoadState 读取应用状态。文件不存在时返回零值，损坏时返回零值和错误。
func LoadState(name string) (State, error) {
	var s State
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// SaveState 原子替换应用状态文件。
func SaveState(name string, s State) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(name, 0o644, bytes.NewReader(data), nil)
}
