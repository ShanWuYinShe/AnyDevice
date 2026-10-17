package util

import (
	"os"

	"github.com/mdp/qrterminal/v3"
)

// PrintQRCode prints a compact QR code for the given URL directly to the terminal stdout.
func PrintQRCode(url string) {
	config := qrterminal.Config{
		Level:      qrterminal.M,
		Writer:     os.Stdout,
		HalfBlocks: true,
		QuietZone:  1,
	}
	qrterminal.GenerateWithConfig(url, config)
}
