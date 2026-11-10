// Command gotyp 启动桌面笔记应用。
package main

import (
	"log"

	"gotyp/internal/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		log.Fatal(err)
	}
}
