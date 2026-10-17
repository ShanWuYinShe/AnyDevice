# ==============================================================================
# AnyDevice - Windows 虚拟音频设备一键安装脚本 (VB-CABLE Driver)
# 请在管理员权限下的 PowerShell 中执行本脚本
# ==============================================================================

Write-Host ">>> AnyDevice Windows 虚拟音频设备安装助手..." -ForegroundColor Cyan

# 检查管理员权限
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[!] 请右键选择以管理员身份运行 PowerShell，然后重新执行此脚本！" -ForegroundColor Red
    Pause
    Exit
}

# 优先尝试 winget 安装
if (Get-Command winget -ErrorAction SilentlyContinue) {
    Write-Host "[*] 检测到 winget，正在尝试通过 winget 静默安装 VB-CABLE..." -ForegroundColor Yellow
    winget install --id VB-Audio.VirtualCable --accept-package-agreements --accept-source-agreements --silent
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[√] VB-CABLE 安装完成！" -ForegroundColor Green
        Write-Host "[*] 请在 Windows 音频设置中将默认输出设备设置为 'CABLE Input'" -ForegroundColor Green
        Exit
    }
}

# 如果没有 winget 或失败，直接从官网下载官方驱动包
Write-Host "[*] 正在从 VB-Audio 官网下载官方驱动包..." -ForegroundColor Yellow
$zipUrl = "https://download.vb-audio.com/Download_VAIO/VBCABLE_Driver_Pack43.zip"
$tempZip = "$env:TEMP\VBCABLE_Driver_Pack43.zip"
$tempDir = "$env:TEMP\VBCABLE_Driver"

Invoke-WebRequest -Uri $zipUrl -OutFile $tempZip -UseBasicParsing
if (Test-Path $tempDir) { Remove-Item -Recurse -Force $tempDir }
Expand-Archive -Path $tempZip -DestinationPath $tempDir -Force

Write-Host "[*] 正在静默安装 64 位驱动..." -ForegroundColor Yellow
$installer = "$tempDir\VBCABLE_Setup_x64.exe"
if (-not (Test-Path $installer)) {
    $installer = "$tempDir\VBCABLE_Setup.exe"
}

Start-Process -FilePath $installer -ArgumentList "-i", "-h" -Wait -Verb RunAs

Write-Host "[√] 虚拟音频驱动安装程序已执行完毕！" -ForegroundColor Green
Write-Host "[*] 提示：如果 Windows 右下角声卡仍显示未就绪，请重启一次电脑以加载驱动。" -ForegroundColor Cyan
