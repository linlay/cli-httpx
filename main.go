package main

import (
	"os"

	"github.com/zengnianmei/httpx/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdout, os.Stderr))
}
