package util

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// AutoSetupVirtualDevice automatically installs and configures a virtual audio device for the host OS.
func AutoSetupVirtualDevice() error {
	switch runtime.GOOS {
	case "windows":
		return autoSetupWindows()
	case "linux":
		return autoSetupLinux()
	case "darwin":
		return autoSetupMacOS()
	default:
		return fmt.Errorf("不支持自动配置的系统: %s", runtime.GOOS)
	}
}

func autoSetupWindows() error {
	fmt.Println(">>> [Windows 全自动配置] 开始安装虚拟音频设备 (VB-CABLE)...")

	// 1. 优先尝试使用 Windows 自带的 winget 静默安装
	if _, err := exec.LookPath("winget"); err == nil {
		fmt.Println("[*] 检测到系统 winget，正在尝试通过 winget 静默安装...")
		cmd := exec.Command("winget", "install", "--id", "VB-Audio.VirtualCable", "--accept-package-agreements", "--accept-source-agreements", "--silent")
		if err := cmd.Run(); err == nil {
			fmt.Println("[√] 通过 winget 安装成功！")
			return nil
		}
		fmt.Println("[-] winget 安装未成功，转入自动下载安装流程...")
	}

	// 2. 自动下载官方驱动安装包
	tempDir := filepath.Join(os.TempDir(), "vbcable_installer")
	_ = os.RemoveAll(tempDir)
	_ = os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	zipPath := filepath.Join(tempDir, "vbcable.zip")
	driverURL := "https://download.vb-audio.com/Download_VAIO/VBCABLE_Driver_Pack43.zip"

	fmt.Printf("[*] 正在从官网下载驱动包: %s\n", driverURL)
	if err := downloadFile(driverURL, zipPath); err != nil {
		return fmt.Errorf("下载驱动包失败: %w", err)
	}

	fmt.Println("[*] 正在解压驱动程序...")
	if err := unzipFile(zipPath, tempDir); err != nil {
		return fmt.Errorf("解压驱动失败: %w", err)
	}

	// 3. 选择适合 64 位或 32 位的安装器
	installerName := "VBCABLE_Setup_x64.exe"
	if runtime.GOARCH == "386" {
		installerName = "VBCABLE_Setup.exe"
	}
	installerPath := filepath.Join(tempDir, installerName)

	if _, err := os.Stat(installerPath); err != nil {
		return fmt.Errorf("未找到安装程序: %s", installerPath)
	}

	fmt.Println("[*] 正在启动静默安装 (如果弹出系统 UAC 提示，请点击「是」允许安装)...")
	// 使用 PowerShell Start-Process 提升管理员权限执行静默安装 (-i 安装, -h 静默)
	psCmd := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList '-i', '-h' -Verb RunAs -Wait", installerPath)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("执行安装器失败: %w", err)
	}

	fmt.Println("[√] 驱动安装完成，等待系统加载音频设备...")
	time.Sleep(3 * time.Second)
	return nil
}

func autoSetupLinux() error {
	fmt.Println(">>> [Linux 全自动配置] 创建 PulseAudio/PipeWire 虚拟声卡 Sink...")
	cmd := exec.Command("pactl", "load-module", "module-null-sink", "sink_name=AnyDeviceSink", "sink_properties=device.description=AnyDevice_Virtual_Speaker")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("创建虚拟 Sink 失败: %s (%w)", string(out), err)
	}

	// 设置为默认输出设备
	_ = exec.Command("pactl", "set-default-sink", "AnyDeviceSink").Run()
	fmt.Println("[√] 虚拟音频设备已创建并设为默认 (AnyDeviceSink)！")
	return nil
}

func autoSetupMacOS() error {
	fmt.Println(">>> [macOS 全自动配置] 检查并安装 BlackHole 虚拟音频驱动...")
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("未检测到 Homebrew，请先安装 brew 或手动运行: brew install blackhole-2ch")
	}

	cmd := exec.Command("brew", "install", "blackhole-2ch")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP 状态码异常: %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func unzipFile(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// PrintVirtualDeviceGuide prints manual fallback guidance if automatic setup fails.
func PrintVirtualDeviceGuide() {
	fmt.Println("\n==================================================================")
	fmt.Println("⚠️  自动配置未能完成，您可以参考以下手动方案：")
	fmt.Println("==================================================================")

	switch runtime.GOOS {
	case "windows":
		fmt.Println("【Windows 手动安装 VB-CABLE 虚拟声卡】：")
		fmt.Println("  1. 管理员运行 PowerShell 执行: winget install --id VB-Audio.VirtualCable")
		fmt.Println("  2. 或访问官网下载: https://vb-audio.com/Cable/")
		fmt.Println("  3. 安装后将 Windows 音量默认输出设为「CABLE Input」即可。")

	case "darwin":
		fmt.Println("【macOS 手动安装】：brew install blackhole-2ch")

	case "linux":
		fmt.Println("【Linux 手动配置】：pactl load-module module-null-sink sink_name=AnyDeviceSink")
	}

	fmt.Println("==================================================================\n")
}
