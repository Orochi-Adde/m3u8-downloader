package main

import (
	"crypto/tls" // 补上这一行
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TsInfo struct {
	Name string
	URL  string
}

var (
	httpClient      *http.Client
	headerMap       = make(map[string]string)
	downloadedBytes int64 // 用于计算下载速度
)

func InitNetwork() {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	if Flags.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// 注入代理 (如果指定了 -proxy 参数，否则读取系统代理)
	if Flags.Proxy != "" {
		proxyURL, err := url.Parse(Flags.Proxy)
		if err != nil {
			log.Fatalf("❌ 代理地址格式错误: %v", err)
		}
		tr.Proxy = http.ProxyURL(proxyURL)
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}

	httpClient = &http.Client{
		Transport: tr,
		Timeout:   time.Duration(Config.TimeoutSec) * time.Second,
	}

	// 构建固定 Header
	headerMap["User-Agent"] = GetRandomUA()
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
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headerMap {
		req.Header.Set(k, v)
	}

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

	// 启动速度监控面板协程
	go speedMonitor(tsLen, &completedCount)

	wg.Wait()
}

func downloadTsWithRetry(ts TsInfo, savePath string, key string, retries int) {
	filePath := filepath.Join(savePath, ts.Name)
	
	// 【新增】断点续传与强制覆盖检测逻辑
	if info, err := os.Stat(filePath); err == nil {
		if Flags.Force {
			// 如果开启了强行覆盖，删掉旧的重新下
			os.Remove(filePath)
		} else if info.Size() > 0 {
			// 文件存在且大小大于0，直接判定为已下载完成，跳过本次下载
			return
		} else {
			// 文件存在但是是0kb空壳，删掉重下
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

		// 找寻同步字节 0x47 (剥离伪装图文头)
		for j := 0; j < len(data); j++ {
			if data[j] == 71 {
				data = data[j:]
				break
			}
		}

		os.WriteFile(filePath, data, 0666)
		atomic.AddInt64(&downloadedBytes, int64(len(data))) // 记录下载量用于测速
		return
	}
}
