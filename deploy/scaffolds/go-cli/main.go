package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	name := flag.String("name", "world", "问候对象")
	flag.Parse()
	fmt.Printf("你好, %s!\n", *name)
	os.Exit(0)
}
