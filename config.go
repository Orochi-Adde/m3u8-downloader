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

// 1. 结构体新增 DefaultDir
type AppConfig struct {
	DefaultThreads int      `json:"default_threads"`
	TimeoutSec     int      `json:"timeout_sec"`
	MaxRetries     int      `json:"max_retries"`
	Proxy          string   `json:"proxy"`
	Insecure       bool     `json:"insecure"`
	DefaultDir     string   `json:"default_dir"` // 【新增这一行】
	UserAgents     []string `json:"user_agents"`
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
		Config = AppConfig{
			DefaultThreads: 32,
			TimeoutSec:     15,
			MaxRetries:     5,
			Proxy:          "",
			Insecure:       false,
			DefaultDir:     "", // 【新增这一行：默认留空，代表当前目录】
			UserAgents: []string{
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
			},
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

	// 2. 【修改点】让命令行的默认值绑定到 Config.DefaultDir
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

	flag.Parse()

	if Flags.URL == "" {
		log.Fatal("❌ 错误: 请提供有效的 M3U8 下载地址 (-u)")
	}

	if Flags.Retries <= 0 {
		Flags.Retries = Config.MaxRetries
	}
}

func GetRandomUA() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	if len(Config.UserAgents) == 0 {
		return "Mozilla/5.0"
	}
	return Config.UserAgents[r.Intn(len(Config.UserAgents))]
}
