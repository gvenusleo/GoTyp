package gui

import "github.com/egoist/mygo/ui"

// renameView 是只有一个输入框的重命名对话框，回车确认，Esc 取消。
func (a *App) renameView(c *ui.Context) {
	if !a.rename.open {
		return
	}
	t := c.Theme()
	ui.DialogBase(c.Key("rename-file"), &a.rename.open, func(backdrop, panel ui.Element) {
		modalPanel(c, backdrop, panel).Width(dialogWidth + 2*t.Space(5)).Padding(t.Space(5)).Gap(t.Space(3)).Label("重命名")
		ui.Text(c, "重命名").Bold()
		if ui.TextInput(c, &a.rename.name).Label("名称").AutoFocus().FillWidth().Submitted() {
			a.commitRename()
		}
		ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() { a.rename.open = false })
			ui.PrimaryButton(c, "重命名").OnClick(a.commitRename)
		})
	})
}
