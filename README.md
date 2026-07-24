# 🚀 高性能 M3U8 流媒体下载器 (M3U8 Downloader)

基于 Go 语言原生标准库打造的极速、轻量级流媒体视频下载工具。专为突破现代视频网站的反爬机制而生，拥有极高的并发性能和极低的内存占用。
### 2026-7-25支持浏览器指纹下载功能，添加-s参数即可
### 2026-4-26重磅更新，支持专用油猴脚本嗅探加密视频，点击添加专用油猴解析脚本[m3u8-sniffer](https://raw.githubusercontent.com/Orochi-Adde/m3u8-downloader/main/m3u8-sniffer.user.js)

## ✨ 核心特性

* ⚡️ **极速并发**：基于 Go 原生 Goroutine (Worker Pool 模型)，榨干宽带极限。
* 🛡️ **强力反爬**：
    * 内置海量真实浏览器 User-Agent 随机池。
    * 支持自动剥离伪装成 `.jpg/.png` 的图片头部，精准提取视频流（0x47 同步字节检测）。
    * 完美支持标准 AES-128 视频流自动解密。
* 🔄 **完美断点续传**：随时按 `Ctrl+C` 安全退出。再次运行相同命令，将以秒级速度校验已下载碎片并无缝续传。
* 📦 **零内存合并**：摒弃传统将所有碎片读入内存的合并方式，采用 `io.Copy` 硬盘级流式拷贝，合并 100GB 视频内存占用也不超 10MB。
* ⚙️ **智能配置分离**：支持 `config.json` 兜底配置与命令行临时参数双重控制（命令行优先级最高）。
* 🌐 **全能网络代理**：全面支持 HTTP/HTTPS/SOCKS5 代理配置，支持强制跳过 HTTPS 证书校验。

---

## 🛠️ 快速开始

### 1. 首次运行与环境初始化
将下载好的压缩包解压，在当前目录下打开命令行终端（Windows 用户可在文件夹顶部地址栏输入 `cmd` 回车）。

直接运行任意命令或双击程序，如果程序检测不到配置文件，会**自动在当前目录生成一个排版整齐的 `config.json`**。

### 2. 基础下载
只需提供一个 `-u` 参数即可开始下载，默认保存为 `movie.mp4`：
```bash
m3u8-downloader -u "https://example.com/video.m3u8"
```

## ⚙️ 配置文件 (config.json)
程序优先读取目录下的 config.json 作为默认配置。你可以随时修改它以适应你的长期网络环境，免去每次敲长命令的烦恼。
```json
{
  "default_threads": 32,      // 默认并发数 (建议 32-64)
  "default_dir": "./"         // 默认下载文件保存目录
  "timeout_sec": 15,          // 碎片下载超时时间 (秒)
  "max_retries": 5,           // 失败重试次数
  "proxy": "",                // 默认全局代理 (例: socks5://127.0.0.1:10808，留空则无代理)
  "insecure": false,          // 默认是否跳过 HTTPS 证书验证
  "user_agents": [            // 随机 UA 池，可自行追加
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36..."
  ]
   "advanced_headers": [    {
      "Accept": "*/*",
	  "priority": "u=1, i",
	  "accept-encoding": "gzip, deflate, br, zstd",
      "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
      "Sec-Ch-Ua": "\"Not;A=Brand\";v=\"8\", \"Chromium\";v=\"146\", \"Google Chrome\";v=\"146\"",
      "Sec-Ch-Ua-Mobile": "?0",
      "Sec-Ch-Ua-Platform": "\"Windows\"",
      "Sec-Fetch-Dest": "empty",
      "Sec-Fetch-Mode": "cors",
      "Sec-Fetch-Site": "cross-site",
      "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
    }]
}
```
## 🎛️ 命令行参数速查表配置
| 缩写 | 完整参数 | 说明 | 示例 |
| :--- | :--- | :--- | :--- |
| **`-u`** | `-url` | **[必填]** M3U8 播放列表的完整下载地址 | `-u "http://xxx/a.m3u8"` |
| **`-o`** | `-name` | 最终生成的视频文件名（无需加 .mp4 后缀） | `-o "阿凡达"` |
| **`-n`** | `-threads` | 临时指定并发下载的线程数 | `-n 64` |
| **`-d`** | `-dir` | 视频及缓存碎片的保存目录（默认在当前目录） | `-d "D:\Downloads"` |
| **`-p`** | `-proxy` | 临时指定代理，**会覆盖配置文件的设置** | `-p "http://127.0.0.1:7890"` |
| **`-H`** | `-header` | 自定义 HTTP 请求头，**可多次叠加使用** | `-H "Referer: https://abc.com"` |
| **`-c`** | `-cookie` | 自定义 Cookie，用于需要登录的鉴权视频 | `-c "session_id=123; uid=456"` |
| **`-k`** | `-insecure` | 强制跳过 HTTPS 证书验证（对付野鸡网站报错） | `-k` |
| **`-f`** | `-force` | **强行覆盖**：若存在同名 mp4，直接删除并重新下载 | `-f` |
| **`-r`** | `-retries` | 临时指定单碎片最大重试次数 | `-r 10` |
| **`-s`** | `-s` | 支持浏览器指纹下载功能 |  |
| 无 | `-keep` | 合并完 `.mp4` 后，保留 `.ts` 碎片不删除 | `-keep` |

## 🎮 进阶使用场景
* **场景1**：极限榨干网速如果你拥有千兆宽带且目标服务器没做限制，可以直接拉起 64 甚至 128 线程暴力下载：
```Bash 
m3u8-downloader -u "https://example.com/video.m3u8(https://example.com/video.m3u8)" -o "高清大片" -n 64
```

* **场景2**：突破防盗链（伪装来源）很多视频网站会校验 Referer（来源网址）和 Cookie。你可以使用 -H 无限叠加请求头来骗过服务器：
```Bash 
m3u8-downloader -u "https://example.com/video.m3u8" -o "突破防盗链" -H "Referer: https://example.com" -H "Origin: https://example.com" -c "vip_token=xxxx"
```

* **场景3**：临时走代理下载特定被墙视频假设你 config.json 里没有配代理，但临时遇到一个需要翻墙才能下的源：
```Bash 
m3u8-downloader -u "https://wall.com/video.m3u8" -p "socks5://127.0.0.1:10808"
```
反之，如果你在 config.json 配置了全局代理，但想临时直连下载国内视频，可以使用 -p "" 清空代理。

* **场景4**：强行洗牌重新下载如果之前下载生成的 mp4 损坏，或者你想重新下载，直接加上 -f 参数，程序会自动铲平旧文件：
```Bash 
m3u8-downloader -u "https://example.com/video.m3u8" -o "老视频" -f
```

## ❓ 常见问题(FAQ)
**Q1**：下载报错 tls: bad record MAC 怎么办？

**A**： 这通常意味着遭到了 Cloudflare 等高级 WAF 的拦截，识别到了 Go 语言的 TLS 握手特征。解决办法： 请务必开启代理，并在命令行中传入代理参数（如  -p "http://127.0.0.1:7890"）。通过代理服务器转发通常能抹平这部分特征差异。


**Q2**：按 Ctrl+C 退出后，下次怎么继续下载？

**A**： 什么都不用改！直接在命令行中按 上箭头 调出上一次运行的完整命令，按下回车即可。程序会自动扫描目录下的碎片，1 秒内恢复到之前的进度并继续下载。


**Q3**：为什么有些视频网站嗅探出来的 M3U8 链接，放进去提示解析失败/找不到碎片？

**A**： 有些复杂的视频网站采用的是“Master M3U8”（主播放列表嵌套），里面包含了不同清晰度（720p, 1080p）的子 M3U8 链接。解决办法： 你需要将那个包含真实 .ts 碎片的“子 M3U8 链接”喂给程序，而不是外层的主列表。通常在浏览器的 Network 面板里找体积最大、不断刷新的那个请求即可。
