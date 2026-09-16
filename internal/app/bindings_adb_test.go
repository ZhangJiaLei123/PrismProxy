package app

import "testing"

func TestSelectResolvedSerial(t *testing.T) {
	cases := []struct {
		name    string
		serials []string
		want    string
		ok      bool
	}{
		{
			name:    "空",
			serials: nil,
			ok:      false,
		},
		{
			name:    "单台雷电双传输（emulator-5554 + 回环 5555）折叠为同机，优先 emulator",
			serials: []string{"emulator-5554", "127.0.0.1:5555"},
			want:    "emulator-5554",
			ok:      true,
		},
		{
			name:    "传输顺序反过来仍能配对",
			serials: []string{"127.0.0.1:5555", "emulator-5554"},
			want:    "emulator-5554",
			ok:      true,
		},
		{
			name:    "仅 emulator 入口",
			serials: []string{"emulator-5554"},
			want:    "emulator-5554",
			ok:      true,
		},
		{
			name:    "仅回环入口（无 emulator console 传输）",
			serials: []string{"127.0.0.1:5555"},
			want:    "127.0.0.1:5555",
			ok:      true,
		},
		{
			name:    "单台 USB 真机",
			serials: []string{"ABCDEF123456"},
			want:    "ABCDEF123456",
			ok:      true,
		},
		{
			name:    "模拟器 + USB 真机：不得折叠，必须报多设备",
			serials: []string{"emulator-5554", "ABCDEF123456"},
			ok:      false,
		},
		{
			name:    "雷电双传输 + USB 真机：仍是两台",
			serials: []string{"emulator-5554", "127.0.0.1:5555", "ABCDEF123456"},
			ok:      false,
		},
		{
			name:    "两台模拟器双开（emulator-5554/5556）：不得静默选第一台",
			serials: []string{"emulator-5554", "emulator-5556"},
			ok:      false,
		},
		{
			name:    "emulator-5554 + 属于另一实例的回环 5557：端口不配对，按两台",
			serials: []string{"emulator-5554", "127.0.0.1:5557"},
			ok:      false,
		},
		{
			name:    "两台雷电各自双传输（5554↔5555、5556↔5557）：两台",
			serials: []string{"emulator-5554", "127.0.0.1:5555", "emulator-5556", "127.0.0.1:5557"},
			ok:      false,
		},
		{
			name:    "两台 USB 真机",
			serials: []string{"DEV1", "DEV2"},
			ok:      false,
		},
		{
			name:    "局域网无线 adb 设备（非 127.0.0.1）与模拟器并存：两台",
			serials: []string{"emulator-5554", "192.168.1.20:5555"},
			ok:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selectResolvedSerial(tc.serials)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (picked=%q)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("picked = %q, want %q", got, tc.want)
			}
		})
	}
}
