package device

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"go.bug.st/serial"

	"git.zk35.de/secalpha/vbus2mqtt/internal/config"
)

// Device wraps a serial port and exposes Read/Close.
type Device struct {
	port serial.Port
	path string
}

// Detect returns the first available USB serial device.
// Checks /dev/ttyUSB* then /dev/ttyACM* (alphabetically sorted).
func Detect() (string, error) {
	patterns := []string{"/dev/ttyUSB*", "/dev/ttyACM*"}
	var found []string
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		found = append(found, matches...)
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no USB serial device found (/dev/ttyUSB*, /dev/ttyACM*)")
	}
	sort.Strings(found)
	return found[0], nil
}

// Open opens the configured serial port, auto-detecting if SERIAL_PORT is empty.
func Open(cfg *config.Config, log *slog.Logger) (*Device, error) {
	port := cfg.SerialPort
	if port == "" {
		var err error
		port, err = Detect()
		if err != nil {
			return nil, err
		}
		log.Info("auto-detected serial device", "port", port)
	}

	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	p, err := serial.Open(port, mode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}

	// ReadTimeout causes Read() to return after 1s even without data.
	// This allows the caller to check context cancellation regularly.
	if err := p.SetReadTimeout(time.Second); err != nil {
		p.Close()
		return nil, fmt.Errorf("set read timeout on %s: %w", port, err)
	}

	log.Info("serial port opened", "port", port, "baud", cfg.BaudRate)
	return &Device{port: p, path: port}, nil
}

func (d *Device) Read(buf []byte) (int, error) {
	return d.port.Read(buf)
}

func (d *Device) Path() string { return d.path }

func (d *Device) Close() {
	_ = d.port.Close()
}
