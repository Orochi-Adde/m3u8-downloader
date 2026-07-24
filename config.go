package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"
)

// 1. 结构体扩展，支持高级请求头组
type AppConfig struct {
	DefaultThreads  int                 `json:"default_threads"`
	TimeoutSec      int                 `json:"timeout_sec"`
	MaxRetries      int                 `json:"max_retries"`
	Proxy           string              `json:"proxy"`
	Insecure        bool                `json:"insecure"`
	DefaultDir      string              `json:"default_dir"` // 【原有】
	UserAgents      []string            `json:"user_agents"`
	AdvancedHeaders []map[string]string `json:"advanced_headers"` // 【新增】复杂 Header 字典数组
}

type GlobalFlags struct {
	URL      string
	OutName  string
	OutDir   string
	Threads  int
	Proxy    string
	Cookie   string
	Insecure bool
	KeepTs   bool
	Headers  sliceFlags
	Force    bool
	Retries  int
	Stealth  bool // 【新增】控制是否启用复杂 Header 的开关
}

type sliceFlags []string

func (i *sliceFlags) String() string { return strings.Join(*i, ", ") }
func (i *sliceFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

var (
	Config AppConfig
	Flags  GlobalFlags
)

func LoadConfigAndFlags() {
	configFile, err := os.ReadFile("config.json")
	if err == nil {
		json.Unmarshal(configFile, &Config)
	} else {
		fmt.Println("⚠️ 未检测到 config.json，正在自动生成默认配置文件...")
		
		// 构建一个默认的复杂 Header 组（以现代 Chrome 为例）
		defaultAdvancedHeader := map[string]string{
			"User-Agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
			"Accept":             "*/*",
			"Accept-Language":    "zh-CN,zh;q=0.9,en;q=0.8",
			"Sec-Fetch-Dest":     "empty",
			"Sec-Fetch-Mode":     "cors",
			"Sec-Fetch-Site":     "cross-site",
			"Sec-Ch-Ua":          `"Not/A)Brand";v="8", "Chromium";v="126", "Google Chrome";v="126"`,
			"Sec-Ch-Ua-Mobile":   "?0",
			"Sec-Ch-Ua-Platform": `"Windows"`,
		}

		Config = AppConfig{
			DefaultThreads: 32,
			TimeoutSec:     15,
			MaxRetries:     5,
			Proxy:          "",
			Insecure:       false,
			DefaultDir:     "", 
			UserAgents: []string{
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
			},
			AdvancedHeaders: []map[string]string{defaultAdvancedHeader}, // 初始化默认的高级配置
		}

		configData, err := json.MarshalIndent(Config, "", "  ")
		if err == nil {
			os.WriteFile("config.json", configData, 0666)
			fmt.Println("✅ 默认配置文件 (config.json) 已成功生成！")
		}
	}

	defaultName := time.Now().Format("video_060102_15-04-05")

	flag.StringVar(&Flags.URL, "url", "", "M3U8下载地址")
	flag.StringVar(&Flags.URL, "u", "", "同 -url")

	flag.StringVar(&Flags.OutName, "name", defaultName, "保存的文件名")
	flag.StringVar(&Flags.OutName, "o", defaultName, "同 -name")

	flag.StringVar(&Flags.OutDir, "dir", Config.DefaultDir, "保存目录")
	flag.StringVar(&Flags.OutDir, "d", Config.DefaultDir, "同 -dir")

	flag.IntVar(&Flags.Threads, "threads", Config.DefaultThreads, "并发线程数")
	flag.IntVar(&Flags.Threads, "n", Config.DefaultThreads, "同 -threads")

	flag.StringVar(&Flags.Proxy, "proxy", Config.Proxy, "设置代理")
	flag.StringVar(&Flags.Proxy, "p", Config.Proxy, "同 -proxy")

	flag.BoolVar(&Flags.Insecure, "insecure", Config.Insecure, "跳过 HTTPS 验证")
	flag.BoolVar(&Flags.Insecure, "k", Config.Insecure, "同 -insecure")

	flag.StringVar(&Flags.Cookie, "cookie", "", "自定义 Cookie")
	flag.StringVar(&Flags.Cookie, "c", "", "同 -cookie")

	flag.BoolVar(&Flags.KeepTs, "keep", false, "保留 ts 碎片")

	flag.Var(&Flags.Headers, "header", "自定义 HTTP 请求头")
	flag.Var(&Flags.Headers, "H", "同 -header")

	flag.BoolVar(&Flags.Force, "force", false, "强行覆盖已存在的文件")
	flag.BoolVar(&Flags.Force, "f", false, "同 -force")

	flag.IntVar(&Flags.Retries, "retries", 0, "最大重试次数")
	flag.IntVar(&Flags.Retries, "r", 0, "同 -retries")

	// 【新增】监听 -s 参数
	flag.BoolVar(&Flags.Stealth, "s", false, "启用完整的浏览器环境特征 Header 伪装")

	flag.Parse()

	if Flags.URL == "" {
		log.Fatal("❌ 错误: 请提供有效的 M3U8 下载地址 (-u)")
	}

	if Flags.Retries <= 0 {
		Flags.Retries = Config.MaxRetries
	}
}

// 保持原有的基础 UA 获取能力
func GetRandomUA() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	if len(Config.UserAgents) == 0 {
		return "Mozilla/5.0"
	}
	return Config.UserAgents[r.Intn(len(Config.UserAgents))]
}

// 【新增】智能获取基础 Header
// 如果指定了 -s，则返回完整的浏览器特征头；否则只返回随机的 User-Agent
func GetBaseHeaders() map[string]string {
	headers := make(map[string]string)
	
	// 如果开启了 -s，且配置文件中存在高级 Header 组
	if Flags.Stealth && len(Config.AdvancedHeaders) > 0 {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		// 随机抽选一组高级 Header
		profile := Config.AdvancedHeaders[r.Intn(len(Config.AdvancedHeaders))]
		for k, v := range profile {
			headers[k] = v
		}
	} else {
		// 未开启 -s，回退到普通模式，仅附带 UA
		headers["User-Agent"] = GetRandomUA()
	}
	
	return headers
}
