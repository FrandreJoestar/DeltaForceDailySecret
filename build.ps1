# 生成 Windows 版本资源后编译
# 用法：
#   .\build.ps1                    # 生成带版本信息的 GUI 版 deltaforce-secret.exe
#   .\build.ps1 -Version 1.2.0.0   # 指定版本号
#   .\build.ps1 -Console           # 生成带控制台的调试版 deltaforce-secret-debug.exe

param(
    [string]$Version = "1.0.0.0",
    [switch]$Console
)

$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot

try {
    $parts = $Version.Split('.')
    if ($parts.Count -ne 4) {
        throw "版本号格式应为 a.b.c.d，例如 1.0.0.0"
    }

    Write-Host "生成版本资源 $Version ..."
    go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 `
        -o resource_windows_amd64.syso `
        -ver-major $parts[0] -ver-minor $parts[1] -ver-patch $parts[2] -ver-build $parts[3] `
        -product-ver-major $parts[0] -product-ver-minor $parts[1] -product-ver-patch $parts[2] -product-ver-build $parts[3] `
        -file-version $Version -product-version $Version `
        versioninfo.json
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "版本资源生成失败（首次使用需要联网下载 goversioninfo），继续编译不带版本信息的 exe"
        Remove-Item resource_windows_amd64.syso -ErrorAction SilentlyContinue
    }

    if ($Console) {
        Write-Host "编译调试版（保留控制台）..."
        go build -o deltaforce-secret-debug.exe .
        $out = 'deltaforce-secret-debug.exe'
    }
    else {
        Write-Host "编译 GUI 版（双击不弹控制台）..."
        go build -trimpath -ldflags "-H windowsgui" -o deltaforce-secret.exe .
        $out = 'deltaforce-secret.exe'
    }
    if ($LASTEXITCODE -ne 0) {
        throw "编译失败"
    }
    Write-Host "构建完成：$out"
}
finally {
    Pop-Location
}
