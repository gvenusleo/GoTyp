# GoTyp

GoTyp 是用 Go 编写的本地笔记应用，界面由 MyGo Native UI 绘制。打开一个文件夹作为笔记仓库，在单窗口里编辑 Markdown 和 Typst 源码，旁边是原生预览。

- Markdown 有编辑、分屏、预览三种模式，支持本地和 HTTP(S) 图片，以及笔记间的相对链接
- Typst 随输入实时更新分页预览，显示编译错误，支持补全和悬停文档。导出 PDF 使用磁盘上已保存的版本
- 仓库里的其他文件也在文件树中：纯文本直接编辑，图片在应用内显示，其余交给系统默认应用
- 在「设置」的「编辑」页可关闭编辑时自动保存。切换标签时可以保留内存中的编辑。关闭标签、切换仓库或退出时，会尝试保存受影响的文件。写入前检查外部修改，再原子替换文件
- 笔记就是仓库里的原始文件，GoTyp 不往仓库里写自己的文件

## 依赖

Typst 预览、PDF 导出和编辑辅助都使用 `PATH` 中的 tinymist。缺少 tinymist 时，仍可编辑和保存 Typst 笔记。[tinymist 项目](https://github.com/Myriad-Dreamin/tinymist)

从访达或程序坞启动的应用拿不到 shell 配置的 `PATH`，所以 GoTyp 启动时会额外查找 `/opt/homebrew/bin`、`/usr/local/bin`、`~/.cargo/bin`、`~/.local/bin` 和 mise 的 shims 目录。

## 构建

使用 [mise](https://mise.jdx.dev) 管理工具链与任务：

```sh
mise run dev       # 启动桌面应用，热重载
mise run build     # 编译安装包，输出到 build/
mise run install   # 编译并安装到 /Applications（macOS）
mise run ci        # 格式检查、vet、测试、编译、漏洞扫描
```

## 数据

应用状态保存在用户配置目录的 `GoTyp/state.json`（macOS 上是 `~/Library/Application Support/GoTyp/state.json`），包括最近打开的仓库，以及每个仓库的打开标签、当前文件、编辑模式和侧栏宽度。删掉它只会回到默认界面，不影响笔记。Typst 预览的临时文件放在系统临时目录。

## 结构

设计见 [docs/design.md](docs/design.md)，开发约定见 [AGENTS.md](AGENTS.md)。

| 包 | 职责 |
| --- | --- |
| `cmd/gotyp` | 组装依赖并启动应用 |
| `internal/vault` | 仓库路径、扫描、读取、原子保存和冲突检查，不依赖界面 |
| `internal/tinymist` | tinymist 进程、编辑辅助、实时预览和已保存笔记的 PDF 导出 |
| `internal/gui` | 应用状态、用户动作和界面 |

## 许可

[MIT](LICENSE)。界面图标来自 [Lucide](https://lucide.dev)，许可见 [internal/gui/icons/LICENSE](internal/gui/icons/LICENSE)。
