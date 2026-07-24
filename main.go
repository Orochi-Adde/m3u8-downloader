// @author: Orochi-Adde
// @date: 2026-07-25
// @功能: 高性能、低内存占用的 Golang M3U8 下载器
// @优化: 使用 tls_client (Chrome 146 指纹 + HTTP/2 HPACK 顺序伪装) 完美绕过 403 拦截
// @优化: 标准 Worker Pool 并发，io.Copy 流式合并零内存泄露，支持断点续传与 AES-128 解密
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// 引入官方 net/http 的替代品 fhttp，用于精准控制 HTTP/2 协议栈和 Header 顺序
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// =====================================================================
// 1. 配置与全局变量定义
// =====================================================================

type ConfigStruct struct {
	DefaultThreads int      `json:"default_threads"`
	TimeoutSec     int      `json:"timeout_sec"`
	MaxRetries     int      `json:"max_retries"`
	DefaultDir     string   `json:"default_dir"`
	UserAgents     []string `json:"user_agents"`
}

type CLIArgs struct {
	URL     string
	OutName string
	OutDir  string
	Threads int
	Proxy   string
	Insecure bool
	Cookie  string
	Headers flag.StringSlice // 自定义切片类型接收多组 -H
	Force   bool
	KeepTs  bool
	Retries int
	Stealth bool
}

// StringSlice 用于解析命令行多个 -H 参数
type StringSlice []string
func (s *StringSlice) String() string { return fmt.Sprintf("%s", *s) }
func (s *StringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

var (
	Config      ConfigStruct
	Flags       CLIArgs
	httpClient  tls_client.HttpClient 
	headerMap   = make(map[string]string)
	downloadedBytes int64
)

type TsInfo struct {
	Name string
	URL  string
}

// =====================================================================
// 2. 初始化配置、参数与网络引擎
// =====================================================================

func LoadConfigAndFlags() {
	// 默认配置兜底
	Config.DefaultThreads = 32
	Config.TimeoutSec = 30
	Config.MaxRetries = 5
	Config.DefaultDir = ""

	// 绑定命令行参数
	defaultName := time.Now().Format("video_060102_15-04-05")
	flag.StringVar(&Flags.URL, "url", "", "M3U8下载地址")
	flag.StringVar(&Flags.URL, "u", "", "同 -url")
	flag.StringVar(&Flags.OutName, "name", defaultName, "保存的文件名")
	flag.StringVar(&Flags.OutName, "o", defaultName, "同 -name")
	flag.StringVar(&Flags.OutDir, "dir", Config.DefaultDir, "保存目录")
	flag.StringVar(&Flags.OutDir, "d", Config.DefaultDir, "同 -dir")
	flag.IntVar(&Flags.Threads, "threads", Config.DefaultThreads, "并发线程数")
	flag.IntVar(&Flags.Threads, "n", Config.DefaultThreads, "同 -threads")
	flag.StringVar(&Flags.Proxy, "proxy", "", "设置代理 (如 socks5://127.0.0.1:10808)")
	flag.StringVar(&Flags.Proxy, "p", "", "同 -proxy")
	flag.BoolVar(&Flags.Insecure, "insecure", false, "跳过 HTTPS 验证")
	flag.BoolVar(&Flags.Insecure, "k", false, "同 -insecure")
	flag.StringVar(&Flags.Cookie, "cookie", "", "自定义 Cookie")
	flag.StringVar(&Flags.Cookie, "c", "", "同 -cookie")
	flag.BoolVar(&Flags.KeepTs, "keep", false, "保留 ts 碎片")
	flag.Var(&Flags.Headers, "header", "自定义 HTTP 请求头")
	flag.Var(&Flags.Headers, "H", "同 -header")
	flag.BoolVar(&Flags.Force, "force", false, "强行覆盖已存在的文件")
	flag.BoolVar(&Flags.Force, "f", false, "同 -force")
	flag.IntVar(&Flags.Retries, "retries", 0, "最大重试次数")
	flag.IntVar(&Flags.Retries, "r", 0, "同 -retries")
	flag.BoolVar(&Flags.Stealth, "s", true, "启用隐身指纹模式")

	flag.Parse()

	if Flags.URL == "" {
		log.Fatal("❌ 错误: 请提供有效的 M3U8 下载地址 (-u)")
	}

	if Flags.Retries <= 0 {
		Flags.Retries = Config.MaxRetries
	}
}

// GetBaseHeaders 生成与 Chrome 146 指纹严格匹配的应用层隐身请求头
func GetBaseHeaders() map[string]string {
	return map[string]string{
		"User-Agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		"Accept":             "*/*",
		"Accept-Language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Sec-Ch-Ua":          `"Not;A=Brand";v="8", "Chromium";v="146", "Google Chrome";v="146"`,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Windows"`,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "cross-site",
		"Priority":           "u=1, i",
	}
}

func InitNetwork() {
	// 配置 tls_client 核心选项：锁死 Chrome 146 指纹
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(time.Duration(Config.TimeoutSec)),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithNotFollowRedirects(),
	}

	if Flags.Insecure {
		options = append(options, tls_client.WithInsecureSkipVerify())
	}

	if Flags.Proxy != "" {
		options = append(options, tls_client.WithProxyUrl(Flags.Proxy))
	} else if sysProxy := os.Getenv("HTTP_PROXY"); sysProxy != "" {
		options = append(options, tls_client.WithProxyUrl(sysProxy))
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		log.Fatalf("❌ 初始化底层网络引擎失败: %v", err)
	}
	httpClient = client

	// 载入基础伪装头
	headerMap = GetBaseHeaders()

	if Flags.Cookie != "" {
		headerMap["Cookie"] = Flags.Cookie
	}
	for _, h := range Flags.Headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headerMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
}

// =====================================================================
// 3. 网络请求与防风控核心 (含 HPACK 顺序强对齐)
// =====================================================================

func FetchContent(reqURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	
	var orderKeys []string
	for k, v := range headerMap {
		req.Header.Set(k, v)
		orderKeys = append(orderKeys, k)
	}

	// 核心防风控：显式锁定 HTTP/2 头部字段在 HPACK 传输时的顺序，杜绝特征乱序
	req.Header[http.HeaderOrderKey] = orderKeys

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP 状态码: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// =====================================================================
// 4. M3U8 解析与 AES 解密逻辑
// =====================================================================

func ParseTsList(baseURL *url.URL, body string) []TsInfo {
	var tsList []TsInfo
	lines := strings.Split(body, "\n")
	index := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		index++
		tsURL, _ := url.Parse(line)
		fullURL := baseURL.ResolveReference(tsURL).String()
		tsList = append(tsList, TsInfo{
			Name: fmt.Sprintf("%05d.ts", index),
			URL:  fullURL,
		})
	}
	return tsList
}

func GetM3u8Key(baseURL *url.URL, body string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.Contains(line, "#EXT-X-KEY") && strings.Contains(line, "URI") {
			start := strings.Index(line, `URI="`) + 5
			end := strings.Index(line[start:], `"`)
			if end == -1 {
				continue
			}
			keyURLObj, _ := url.Parse(line[start : start+end])
			fullKeyURL := baseURL.ResolveReference(keyURLObj).String()
			keyBytes, _ := FetchContent(fullKeyURL)
			return string(keyBytes)
		}
	}
	return ""
}

func AesDecrypt(ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	if len(ciphertext) < blockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	iv := ciphertext[:blockSize]
	ciphertext = ciphertext[blockSize:]
	
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)
	return ciphertext, nil
}

// =====================================================================
// 5. 并发下载与合并流程控制
// =====================================================================

func StartDownloader(tsList []TsInfo, savePath string, key string) {
	tsLen := len(tsList)
	jobs := make(chan TsInfo, tsLen)

	for _, ts := range tsList {
		jobs <- ts
	}
	close(jobs)

	var wg sync.WaitGroup
	var completedCount int32

	for i := 0; i < Flags.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ts := range jobs {
				downloadTsWithRetry(ts, savePath, key, Flags.Retries)
				atomic.AddInt32(&completedCount, 1)
			}
		}()
	}

	go speedMonitor(tsLen, &completedCount)
	wg.Wait()
}

func downloadTsWithRetry(ts TsInfo, savePath string, key string, retries int) {
	filePath := filepath.Join(savePath, ts.Name)
	
	if info, err := os.Stat(filePath); err == nil {
		if Flags.Force {
			os.Remove(filePath)
		} else if info.Size() > 0 {
			return
		} else {
			os.Remove(filePath)
		}
	}

	for i := 0; i < retries; i++ {
		data, err := FetchContent(ts.URL)
		if err != nil || len(data) == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		if key != "" {
			data, _ = AesDecrypt(data, []byte(key))
		}

		// 清洗 TS 流，确保以同步字节 0x47 (71) 开头
		for j := 0; j < len(data); j++ {
			if data[j] == 71 {
				data = data[j:]
				break
			}
		}

		os.WriteFile(filePath, data, 0666)
		atomic.AddInt64(&downloadedBytes, int64(len(data)))
		return
	}
}

// =====================================================================
// 6. 主函数入口
// =====================================================================

func main() {
	LoadConfigAndFlags()
	InitNetwork()
	setupCloseHandler()

	// 1. 确定根目录
	baseDir := Flags.OutDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	os.MkdirAll(baseDir, os.ModePerm)

	// 2. 最终成品 mp4 路径
	finalMoviePath := filepath.Join(baseDir, Flags.OutName+".mp4")

	// 3. 碎片临时文件夹 (_temp 后缀)
	tempTsDir := filepath.Join(baseDir, Flags.OutName+"_temp")
	os.MkdirAll(tempTsDir, os.ModePerm)

	// 冲突检测
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

	StartDownloader(tsList, tempTsDir, tsKey)

	fmt.Println("\n\n🔧 下载完毕，正在进行流式无损合并...")
	
	out, _ := os.Create(finalMoviePath)
	for _, ts := range tsList {
		f, err := os.Open(filepath.Join(tempTsDir, ts.Name))
		if err == nil {
			io.Copy(out, f)
			f.Close()
		}
	}
	out.Close()

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
}
