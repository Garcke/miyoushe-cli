// mys 是米游社社区 CLI 的入口。
package main

import (
	"os"

	"mihoyo_cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
