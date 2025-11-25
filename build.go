// Usage: go run build.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	projectName = "relify"
	mainPath    = "./cmd/relify"
	outputDir   = "./bin"
)

type target struct {
	os, arch, output string
}

func main() {
	os.RemoveAll(outputDir)
	os.MkdirAll(outputDir, 0755)

	version := getVersion()
	buildTime := time.Now().Format("2006-01-02_15:04:05")
	ldflags := fmt.Sprintf("-s -X main.Version=%s -X main.BuildTime=%s", version, buildTime)

	targets := []target{
		// Windows
		{"windows", "amd64", projectName + "-windows-amd64.exe"},
		{"windows", "arm64", projectName + "-windows-arm64.exe"},
		// Linux
		{"linux", "amd64", projectName + "-linux-amd64"},
		{"linux", "arm64", projectName + "-linux-arm64"},
		// Darwin (macOS)
		{"darwin", "amd64", projectName + "-darwin-amd64"},
		{"darwin", "arm64", projectName + "-darwin-arm64"},
	}

	fmt.Printf("🚀 开始构建 %s (版本: %s)\n", projectName, version)
	fmt.Printf("📂 输出目录: %s\n\n", outputDir)

	failed := false
	for i, t := range targets {
		fmt.Printf("[%d/%d] 正在构建 %s/%s -> %s ... ", i+1, len(targets), t.os, t.arch, t.output)

		cmd := exec.Command("go", "build", "-o", filepath.Join(outputDir, t.output), "-ldflags", ldflags, mainPath)
		cmd.Env = append(os.Environ(), "GOOS="+t.os, "GOARCH="+t.arch, "CGO_ENABLED=0")

		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("❌ 失败\n")
			fmt.Printf("错误详情:\n%s\n", string(out))
			failed = true
		} else {
			fmt.Printf("✅ 成功\n")
		}
	}

	fmt.Println()
	if failed {
		fmt.Println("构建任务失败")
		os.Exit(1)
	}
	fmt.Println("构建任务完成")
}

func getVersion() string {
	if out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		return "dev-" + strings.TrimSpace(string(out))
	}
	return "dev"
}
