package mqtt

import (
	"testing"
)

func TestHaClassFromUnit(t *testing.T) {
	tests := []struct {
		unit        string
		wantClass   string
		wantState   string
	}{
		{"°C", "temperature", "measurement"},
		{"K", "temperature", "measurement"},
		{"W", "power", "measurement"},
		{"kW", "power", "measurement"},
		{"Wh", "energy", "total_increasing"},
		{"kWh", "energy", "total_increasing"},
		{"V", "voltage", "measurement"},
		{"bar", "pressure", "measurement"},
		{"l/h", "volume_flow_rate", "measurement"},
		{"%", "", "measurement"},
		{"h", "", "measurement"},
		{"", "", "measurement"},
	}
	for _, tt := range tests {
		dc, sc := haClassFromUnit(tt.unit)
		if dc != tt.wantClass {
			t.Errorf("haClassFromUnit(%q) device_class = %q, want %q", tt.unit, dc, tt.wantClass)
		}
		if sc != tt.wantState {
			t.Errorf("haClassFromUnit(%q) state_class = %q, want %q", tt.unit, sc, tt.wantState)
		}
	}
}

func TestFieldSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Temp S1", "temp_s1"},
		{"Flow Rate", "flow_rate"},
		{"sensor1", "sensor1"},
		{"°C Sensor", "c_sensor"},
		{"  leading  ", "leading"},
		{"heat.meter/kwh", "heat_meter_kwh"},
	}
	for _, tt := range tests {
		got := fieldSlug(tt.input)
		if got != tt.want {
			t.Errorf("fieldSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
