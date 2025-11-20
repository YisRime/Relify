# Relify 构建脚本 - 同时编译 Windows 和 Linux 版本

$ErrorActionPreference = "Stop"

$ProjectName = "relify"
$MainPath = ".\cmd\relify"
$OutputDir = ".\bin"

Write-Host "=== Relify 跨平台构建 ===" -ForegroundColor Cyan
Write-Host ""

# 创建输出目录
if (!(Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir | Out-Null
}

# 获取版本信息（可选）
$Version = git describe --tags --always 2>$null
if (!$Version) {
    $Version = "dev"
}
$BuildTime = Get-Date -Format "yyyy-MM-dd_HH:mm:ss"

Write-Host "版本: $Version" -ForegroundColor Green
Write-Host "构建时间: $BuildTime" -ForegroundColor Green
Write-Host ""

# 构建 Windows amd64 版本
Write-Host "[1/4] 构建 Windows amd64..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -o "$OutputDir\${ProjectName}-windows-amd64.exe" -ldflags "-s -w -X main.Version=$Version -X main.BuildTime=$BuildTime" $MainPath
if ($LASTEXITCODE -eq 0) {
    Write-Host "✓ Windows amd64 构建成功" -ForegroundColor Green
} else {
    Write-Host "✗ Windows amd64 构建失败" -ForegroundColor Red
    exit 1
}

# 构建 Windows arm64 版本
Write-Host "[2/4] 构建 Windows arm64..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "arm64"
$env:CGO_ENABLED = "0"
go build -o "$OutputDir\${ProjectName}-windows-arm64.exe" -ldflags "-s -w -X main.Version=$Version -X main.BuildTime=$BuildTime" $MainPath
if ($LASTEXITCODE -eq 0) {
    Write-Host "✓ Windows arm64 构建成功" -ForegroundColor Green
} else {
    Write-Host "✗ Windows arm64 构建失败" -ForegroundColor Red
    exit 1
}

# 构建 Linux amd64 版本
Write-Host "[3/4] 构建 Linux amd64..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -o "$OutputDir\${ProjectName}-linux-amd64" -ldflags "-s -w -X main.Version=$Version -X main.BuildTime=$BuildTime" $MainPath
if ($LASTEXITCODE -eq 0) {
    Write-Host "✓ Linux amd64 构建成功" -ForegroundColor Green
} else {
    Write-Host "✗ Linux amd64 构建失败" -ForegroundColor Red
    exit 1
}

# 构建 Linux arm64 版本
Write-Host "[4/4] 构建 Linux arm64..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "arm64"
$env:CGO_ENABLED = "0"
go build -o "$OutputDir\${ProjectName}-linux-arm64" -ldflags "-s -w -X main.Version=$Version -X main.BuildTime=$BuildTime" $MainPath
if ($LASTEXITCODE -eq 0) {
    Write-Host "✓ Linux arm64 构建成功" -ForegroundColor Green
} else {
    Write-Host "✗ Linux arm64 构建失败" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "=== 构建完成 ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "输出文件:" -ForegroundColor Green
Get-ChildItem $OutputDir | ForEach-Object {
    $size = [math]::Round($_.Length / 1MB, 2)
    Write-Host "  $($_.Name) ($size MB)" -ForegroundColor White
}
