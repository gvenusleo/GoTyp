# GoTyp

GoTyp 是用 Go 编写的本地笔记应用，界面由 MyGo Native UI 绘制。打开一个文件夹作为笔记仓库，在单窗口里编辑 Markdown 和 Typst 源码，旁边是原生预览。

- Markdown 有编辑、分屏、预览三种模式，本地图片和笔记间的相对链接可以直接用
- Typst 在后台编译，分页预览，显示编译错误，可以导出 PDF；装了 tinymist 时有补全和悬停文档
- 仓库里的其他文件也在文件树中：纯文本直接编辑，图片在应用内显示，其余交给系统默认应用
- 自动保存先写临时文件再原子替换，其他程序改过的文件不会被静默覆盖
- 笔记就是仓库里的原始文件，GoTyp 不往仓库里写自己的文件

## 依赖

Typst 预览和导出需要 `PATH` 中的 [Typst](https://typst.app) CLI，补全和悬停需要 [tinymist](https://github.com/Myriad-Dreamin/tinymist)。两者都可以不装：没有 typst 时 Typst 笔记照样编辑和保存，没有 tinymist 时只是少了补全和悬停。目前在 Typst 0.15.1 上验证过。

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

应用状态保存在用户配置目录的 `GoTyp/state.json`（macOS 上是 `~/Library/Application Support/GoTyp/state.json`），包括最近打开的仓库，以及每个仓库上次打开的文件、编辑模式和侧栏宽度。删掉它只会回到默认界面，不影响笔记。Typst 预览的临时文件放在系统临时目录。

## 结构

设计见 [docs/design.md](docs/design.md)，开发约定见 [AGENTS.md](AGENTS.md)。

| 包 | 职责 |
| --- | --- |
| `cmd/gotyp` | 组装依赖并启动应用 |
| `internal/vault` | 仓库路径、扫描、读取、原子保存和冲突检查，不依赖界面 |
| `internal/typst` | 调用 Typst CLI 编译、取消和读取输出 |
| `internal/tinymist` | tinymist 进程、JSON-RPC、文本同步和坐标换算 |
| `internal/gui` | 应用状态、用户动作和界面 |

## 许可

[MIT](LICENSE)。界面图标来自 [Lucide](https://lucide.dev)，许可见 [internal/gui/icons/LICENSE](internal/gui/icons/LICENSE)。
