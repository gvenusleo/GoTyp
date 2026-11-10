package gui

import "github.com/egoist/mygo/ui"

// renameView 是只有一个输入框的重命名对话框，回车确认，Esc 取消。
func (a *App) renameView(c *ui.Context) {
	if !a.rename.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.rename.open, func() {
		ui.Column(c).Width(dialogWidth).Gap(t.Space(3)).Children(func() {
			ui.Text(c, "重命名").Bold()
			if ui.TextInput(c, &a.rename.name).Label("名称").AutoFocus().FillWidth().Submitted() {
				a.commitRename()
			}
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").OnClick(func() { a.rename.open = false })
				ui.PrimaryButton(c, "重命名").OnClick(a.commitRename)
			})
		})
	})
}
