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

	// 【修改点 1】引入底层的 HTTP/2 控制和 TLS 指纹伪造库
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type TsInfo struct {
	Name string
	URL  string
}

var (
	// 【修改点 2】将原本的 *http.Client 替换为 tls_client.HttpClient
	httpClient      tls_client.HttpClient 
	headerMap       = make(map[string]string)
	downloadedBytes int64 // 用于计算下载速度
)

func InitNetwork() {
	// 【修改点 3】使用 tls_client 配置项替代原本的 http.Transport
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(Config.TimeoutSec),
		// 核心：强制指定 TLS/HTTP2 指纹为 Chrome 146
		tls_client.WithClientProfile(profiles.Chrome_146),
	}

	// 注入是否跳过证书验证选项
	if Flags.Insecure {
		options = append(options, tls_client.WithInsecureSkipVerify())
	}

	// 注入代理 (如果指定了 -proxy 参数)
	if Flags.Proxy != "" {
		// tls-client 直接接受字符串格式的代理
		options = append(options, tls_client.WithProxyUrl(Flags.Proxy))
	} else if sysProxy := os.Getenv("HTTP_PROXY"); sysProxy != "" {
		// 回退读取系统代理
		options = append(options, tls_client.WithProxyUrl(sysProxy))
	}

	// 实例化具备真实浏览器指纹的 Client
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		log.Fatalf("❌ 初始化底层网络引擎失败: %v", err)
	}
	httpClient = client

	// 通过 GetBaseHeaders 获取基础 UA 或完整的复杂特征 Header 组[cite: 7]
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

func FetchContent(reqURL string) ([]byte, error) {
	// 【修改点 4】这里使用的是 fhttp.NewRequest，而不是标准库的 net/http[cite: 4]
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	
	for k, v := range headerMap {
		req.Header.Set(k, v)
	}

	// 发起带有完整浏览器特征的请求
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
// 下方解析 M3U8、解密、并发下载的代码逻辑完全保持不变，因为它们不涉及底层网络连接
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
				downloadTsWithRetry(ts, savePath, key, Config.MaxRetries)
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
