package main

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// 引入官方 net/http 的替代品 fhttp，支持精准控制 HTTP/2 协议栈和 Header 顺序
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// TsInfo 定义单个 TS 视频切片的元数据结构
type TsInfo struct {
	Name string
	URL  string
}

var (
	// httpClient 是全局复用的底层 TLS 客户端，具备浏览器指纹特征，支持 HTTP/2 多路复用
	httpClient      tls_client.HttpClient 
	// headerMap 存储全局的基础请求头（如 User-Agent、Sec-Ch-Ua 等）
	headerMap       = make(map[string]string)
	// downloadedBytes 原子计数器，用于实时计算下载速度和总下载量
	downloadedBytes int64 
)

// InitNetwork 初始化底层的带指纹的网络客户端
func InitNetwork() {
	// 配置 tls_client 的核心选项
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(Config.TimeoutSec),
		// 核心防风控点：强制指定 TLS 握手及 HTTP/2 帧特征为真实的 Chrome 146 浏览器
		tls_client.WithClientProfile(profiles.Chrome_146),
	}

	// 如果开启了跳过证书验证
	if Flags.Insecure {
		options = append(options, tls_client.WithInsecureSkipVerify())
	}

	// 注入代理配置（优先使用命令行指定的 -proxy，其次读取环境变量）
	if Flags.Proxy != "" {
		options = append(options, tls_client.WithProxyUrl(Flags.Proxy))
	} else if sysProxy := os.Getenv("HTTP_PROXY"); sysProxy != "" {
		options = append(options, tls_client.WithProxyUrl(sysProxy))
	}

	// 实例化带有防检测指纹的 HttpClient
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		log.Fatalf("❌ 初始化底层网络引擎失败: %v", err)
	}
	httpClient = client

	// 获取基础的浏览器隐身特征头（包含 User-Agent、Sec-Ch-Ua、Sec-Fetch 等）
	headerMap = GetBaseHeaders()

	// 动态追加命令行传入的 Cookie
	if Flags.Cookie != "" {
		headerMap["Cookie"] = Flags.Cookie
	}
	// 动态追加命令行传入的自定义 Header (-H)
	for _, h := range Flags.Headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headerMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
}

// FetchContent 发起带有完整浏览器特征与顺序控制的 HTTP 请求
func FetchContent(reqURL string) ([]byte, error) {
	// 使用 fhttp 创建请求对象，而非标准库 net/http
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	
	// 依次填充请求头并收集 Key 列表，用于后续的严格排序
	var orderKeys []string
	for k, v := range headerMap {
		req.Header.Set(k, v)
		orderKeys = append(orderKeys, k)
	}

	// 【核心防风控点】：显式指定 HTTP/2 头部字段在 HPACK 压缩时的发送顺序。
	// 高级 WAF（如 Cloudflare）会校验 Header 的顺序是否与真实浏览器一致，乱序会直接触发 403。
	req.Header[http.HeaderOrderKey] = orderKeys

	// 通过具有 Chrome 146 指纹的客户端发送请求
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

// ParseTsList 解析 M3U8 播放列表文本，提取出所有 TS 切片的下载直链
func ParseTsList(baseURL *url.URL, body string) []TsInfo {
	var tsList []TsInfo
	lines := strings.Split(body, "\n")
	index := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 过滤空行和 M3U8 标签行
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

// GetM3u8Key 检查并获取加密视频的 AES Key 秘钥内容
func GetM3u8Key(baseURL *url.URL, body string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		// 查找 #EXT-X-KEY 标签中的 URI 路径
		if strings.Contains(line, "#EXT-X-KEY") && strings.Contains(line, "URI") {
			start := strings.Index(line, `URI="`) + 5
			end := strings.Index(line[start:], `"`)
			if end == -1 {
				continue
			}
			keyURLObj, _ := url.Parse(line[start : start+end])
			fullKeyURL := baseURL.ResolveReference(keyURLObj).String()
			// 请求下载秘钥二进制数据
			keyBytes, _ := FetchContent(fullKeyURL)
			return string(keyBytes)
		}
	}
	return ""
}

// StartDownloader 任务分发器：利用 Goroutine 池实现多线程并发下载
func StartDownloader(tsList []TsInfo, savePath string, key string) {
	tsLen := len(tsList)
	jobs := make(chan TsInfo, tsLen)

	// 将所有切片任务压入 Channel 队列
	for _, ts := range tsList {
		jobs <- ts
	}
	close(jobs)

	var wg sync.WaitGroup
	var completedCount int32

	// 根据命令行指定的并发数（-n）启动工作协程
	for i := 0; i < Flags.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ts := range jobs {
				downloadTsWithRetry(ts, savePath, key, Config.MaxRetries)
				atomic.AddInt32(&completedCount, 1)
			}
		}()
	}

	// 启动后台速度和进度监控
	go speedMonitor(tsLen, &completedCount)
	wg.Wait()
}

// downloadTsWithRetry 负责单个 TS 切片的下载、断点续传检测、AES 解密及 TS 流对齐清洗
func downloadTsWithRetry(ts TsInfo, savePath string, key string, retries int) {
	filePath := filepath.Join(savePath, ts.Name)
	
	// 断点续传/本地缓存检查逻辑
	if info, err := os.Stat(filePath); err == nil {
		if Flags.Force {
			os.Remove(filePath) // 强制覆盖
		} else if info.Size() > 0 {
			return // 文件已存在且不为空，跳过下载
		} else {
			os.Remove(filePath)
		}
	}

	// 失败重试循环
	for i := 0; i < retries; i++ {
		data, err := FetchContent(ts.URL)
		if err != nil || len(data) == 0 {
			time.Sleep(1 * time.Second) // 失败后等待 1 秒再重试
			continue
		}

		// 如果该视频被 AES 加密，执行解密操作
		if key != "" {
			data, _ = AesDecrypt(data, []byte(key))
		}

		// TS 流清洗：寻找 TS 包同步字节 0x47 (十进制 71)，切掉前面的杂质垃圾数据，保证视频能正常播放
		for j := 0; j < len(data); j++ {
			if data[j] == 71 {
				data = data[j:]
				break
			}
		}

		// 写入本地磁盘文件
		os.WriteFile(filePath, data, 0666)
		// 累加已下载字节数
		atomic.AddInt64(&downloadedBytes, int64(len(data)))
		return
	}
}
