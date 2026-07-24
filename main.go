// @author: Orochi-Adde
// @date: 2026-07-25
// @功能: 高性能、低内存占用的 Golang M3U8 下载器
// @优化: 使用 tls_client (Chrome 146 指纹 + HTTP/2 HPACK 顺序伪装) 完美绕过 403 拦截，使用-s参数即可
// @优化: 标准 Worker Pool 并发，io.Copy 流式合并零内存泄露，支持断点续传与 AES-128 解密
package main

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	LoadConfigAndFlags()
	InitNetwork()
	setupCloseHandler()

	// 1. 确定根目录 (最终 mp4 保存的地方)
	baseDir := Flags.OutDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	os.MkdirAll(baseDir, os.ModePerm)

	// 2. 确定最终的 mp4 文件路径
	finalMoviePath := filepath.Join(baseDir, Flags.OutName+".mp4")

	// 3. 建立专门的 ts 碎片临时文件夹 (加 _temp 后缀)
	tempTsDir := filepath.Join(baseDir, Flags.OutName+"_temp")
	os.MkdirAll(tempTsDir, os.ModePerm)

	// 冲突检测：看最终的 mp4 是否已经存在
	if _, err := os.Stat(finalMoviePath); err == nil {
		if Flags.Force {
			fmt.Printf("⚠️ 警告: 检测到已存在的成品文件 [%s.mp4]，将强行删除并重新下载。\n", Flags.OutName)
			os.Remove(finalMoviePath)
		} else {
			log.Fatalf("❌ 文件冲突: 发现已合并完毕的 [%s.mp4]！\n如果您想重新下载覆盖，请加上 -f 或 -force 参数。", Flags.OutName)
		}
	}

	printConfigBanner()

	m3u8Body, err := FetchContent(Flags.URL)
	if err != nil {
		log.Fatalf("❌ 无法获取 M3U8: %v", err)
	}

	baseURL, _ := url.Parse(Flags.URL)
	tsKey := GetM3u8Key(baseURL, string(m3u8Body))
	tsList := ParseTsList(baseURL, string(m3u8Body))

	fmt.Printf("➤ 发现视频碎片: %d 个\n", len(tsList))
	if tsKey != "" {
		fmt.Printf("➤ 解密状态: 已获取 AES-128 密钥\n")
	}
	fmt.Println("--------------------------------------------------")

	// 传递 tempTsDir 给下载器，碎片全放进临时文件夹
	StartDownloader(tsList, tempTsDir, tsKey)

	fmt.Println("\n\n🔧 下载完毕，正在进行流式无损合并...")
	
	out, _ := os.Create(finalMoviePath)
	for _, ts := range tsList {
		// 从临时文件夹读取碎片进行合并
		f, err := os.Open(filepath.Join(tempTsDir, ts.Name))
		if err == nil {
			io.Copy(out, f)
			f.Close()
		}
	}
	out.Close()

	// 清理逻辑变得极其干净：直接连锅端删掉整个临时文件夹
	if !Flags.KeepTs {
		os.RemoveAll(tempTsDir)
		fmt.Println("🗑️ 临时碎片文件夹已清理")
	}

	fmt.Printf("✅ 搞定！文件保存在: %s\n", finalMoviePath)
}

func printConfigBanner() {
	proxyStatus := Flags.Proxy
	if proxyStatus == "" {
		proxyStatus = "直连 (或跟随系统全局)"
	}
	fmt.Println("\n================ M3U8 Downloader ================")
	fmt.Printf("🎯 目标 URL  : %s\n", Flags.URL)
	fmt.Printf("📂 保存名称  : %s.mp4\n", Flags.OutName)
	fmt.Printf("🚀 并发线程  : %d\n", Flags.Threads)
	fmt.Printf("🔄 错误重试  : %d 次\n", Flags.Retries)
	fmt.Printf("🛡️ 强行覆盖  : %t\n", Flags.Force)
	fmt.Printf("🕵️ 隐身模式(-s): %t\n", Flags.Stealth)
	fmt.Printf("🌐 代理配置  : %s\n", proxyStatus)
	
	// 打印完整的请求头，用于 Debug
	fmt.Println("--------------------------------------------------")
	fmt.Println("📋 [DEBUG] 当前生效的完整请求头 (Headers):")
	for k, v := range headerMap {
		fmt.Printf("    • %s: %s\n", k, v)
	}
	fmt.Println("==================================================")
}

func speedMonitor(total int, completed *int32) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	var lastBytes int64

	for range ticker.C {
		currentCompleted := atomic.LoadInt32(completed)
		currentBytes := atomic.LoadInt64(&downloadedBytes)

		speed := float64(currentBytes-lastBytes) / 1024 / 1024
		lastBytes = currentBytes

		percent := float32(currentCompleted) / float32(total)
		pos := int(percent * 30)
		fmt.Printf("\r[下载/校验中] %s%*s %6.2f%% | 速度: %5.2f MB/s | 完成: %d/%d ",
			strings.Repeat("■", pos), 30-pos, "", percent*100, speed, currentCompleted, total)

		if currentCompleted == int32(total) {
			break
		}
	}
}

func setupCloseHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		fmt.Println("\n\n⚠️ 检测到终止信号 (Ctrl+C)，已保存当前断点进度，程序退出。下次启动将自动续传。")
		os.Exit(0)
	}()
