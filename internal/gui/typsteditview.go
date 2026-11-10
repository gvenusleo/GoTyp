package gui

import "github.com/egoist/mygo/ui"

// symbolRenameView 是重命名符号的对话框，与重命名文件的对话框同一种样式。
func (a *App) symbolRenameView(c *ui.Context) {
	if !a.symbolRename.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.symbolRename.open, func() {
		ui.Column(c).Width(dialogWidth).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "重命名符号").Bold()
			if ui.TextInput(c, &a.symbolRename.name).Label("新名称").AutoFocus().FillWidth().Submitted() {
				a.commitSymbolRename()
			}
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").OnClick(func() { a.symbolRename = symbolRenameState{} })
				ui.PrimaryButton(c, "重命名").OnClick(a.commitSymbolRename)
			})
		})
	})
}
