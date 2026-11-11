// selecttest 是取词自检工具（对应文档 M0）：等 N 秒后取当前选区并打印结果。
//
//	go run ./cmd/selecttest --wait 3 --verbose
package main

import (
	"flag"
	"fmt"
	"time"

	"magpie-translate/internal/pick"
	"magpie-translate/internal/winapi"
)

func main() {
	wait := flag.Int("wait", 3, "等待秒数，给用户切窗口/划词的时间")
	verbose := flag.Bool("verbose", false, "打印剪贴板轮询细节")
	blacklist := flag.String("blacklist", pick.DefaultBlacklist, "逗号分隔的进程黑名单")
	flag.Parse()

	fmt.Printf("about to pick in %ds, switch to the target window and select text...\n", *wait)
	time.Sleep(time.Duration(*wait) * time.Second)

	start := time.Now()
	items, err := winapi.SnapshotClipboard()
	fmt.Printf("clipboard formats saved: %d (err=%v)\n", len(items), err)

	p := pick.New(*blacklist, true)
	res, err := p.Pick()
	fmt.Printf("app=%s via=%s ms=%d err=%v\n", res.App, res.Via, time.Since(start).Milliseconds(), err)
	if *verbose {
		fmt.Printf("clipboard seq now=%d text=%q\n", winapi.ClipboardSeq(), truncate(res.Text, 80))
	}
	if res.Text != "" {
		fmt.Printf("text(%d chars): %q\n", len([]rune(res.Text)), truncate(res.Text, 80))
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
