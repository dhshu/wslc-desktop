# 安装 wslc-desktop 到标准用户程序目录并创建开始菜单快捷方式
#
# 为什么需要这个脚本？
# Windows SmartScreen/Defender 对未签名 exe 在「工作区/临时目录」（如 D:\）上的
# 双击启动有拦截；但放在 %LOCALAPPDATA%\Programs\（标准"已安装程序"位置）时放行。
# 本脚本把 exe 复制到标准位置并注册开始菜单入口，双击即可正常打开。
#
# 用法（管理员或普通用户 PowerShell 均可）：
#   powershell -ExecutionPolicy Bypass -File .\install.ps1

$ErrorActionPreference = "Stop"

$SourceExe = Join-Path $PSScriptRoot "build\bin\wslc-desktop.exe"
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\wslc-desktop"
$AppExe = Join-Path $InstallDir "wslc-desktop.exe"
$ShortcutName = "Wslc Desktop"
$StartMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"

$sourceExists = Test-Path $SourceExe
if ($sourceExists -eq $false) {
    Write-Error "找不到源 exe：$SourceExe（请先运行 wails build -s）"
    exit 1
}

# 1. 关闭正在运行的实例，避免文件被占用
Get-Process -Name wslc-desktop -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

# 2. 复制到标准位置
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
Copy-Item $SourceExe -Destination $AppExe -Force

# 3. 解除 Mark-of-the-Web（如果存在）
Unblock-File -Path $AppExe -ErrorAction SilentlyContinue

# 4. 创建开始菜单快捷方式
$shell = New-Object -ComObject WScript.Shell
$shortcutPath = Join-Path $StartMenu "$ShortcutName.lnk"
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $AppExe
$shortcut.WorkingDirectory = $InstallDir
$shortcut.Description = "Wslc Desktop - Windows 容器 CLI 图形前端"
$shortcut.Save()

Write-Host ""
Write-Host "安装完成" -ForegroundColor Green
Write-Host "  程序: $AppExe"
Write-Host "  快捷方式: $shortcutPath"
Write-Host ""
Write-Host "现在可以从开始菜单点击 Wslc Desktop 启动，或直接双击 exe。" -ForegroundColor Cyan
