package access

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"fastiot/internal/ems/profile"
)

type BatchDevice struct {
	ID    string
	Name  string
	Bound bool
}

type BatchChannel struct {
	ID       string
	Name     string
	Occupied map[int]bool
}

type BatchInventory struct {
	Devices  []BatchDevice
	Channels []BatchChannel
	Profiles []profile.Profile
}

// BindingRow is both the preflight result and the per-row apply report. Errors
// never get hidden by a batch-level success count.
type BindingRow struct {
	Line          int      `json:"line"`
	Device        string   `json:"device"`
	Model         string   `json:"model"`
	Channel       string   `json:"channel"`
	Station       int      `json:"station"`
	DeviceID      string   `json:"device_id,omitempty"`
	ProfileID     string   `json:"profile_id,omitempty"`
	ChannelID     string   `json:"channel_id,omitempty"`
	Valid         bool     `json:"valid"`
	Status        string   `json:"status"`
	Errors        []string `json:"errors"`
	CreatedPoints int      `json:"created_points"`
	Mapped        int      `json:"mapped"`
	Missing       []string `json:"missing"`
}

func PreflightCSV(text string, inventory BatchInventory) ([]BindingRow, error) {
	reader := csv.NewReader(bytes.NewReader([]byte(strings.TrimPrefix(text, string(rune(0xFEFF))))))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid_csv")
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("empty_or_invalid_csv")
	}
	columns, err := bindingColumns(records[0])
	if err != nil {
		return nil, err
	}
	devices := map[string][]BatchDevice{}
	channels := map[string][]BatchChannel{}
	profiles := map[string][]profile.Profile{}
	for _, device := range inventory.Devices {
		devices[normalizedReference(device.ID)] = append(devices[normalizedReference(device.ID)], device)
		if device.Name != device.ID {
			devices[normalizedReference(device.Name)] = append(devices[normalizedReference(device.Name)], device)
		}
	}
	for _, channel := range inventory.Channels {
		channels[normalizedReference(channel.ID)] = append(channels[normalizedReference(channel.ID)], channel)
		if channel.Name != channel.ID {
			channels[normalizedReference(channel.Name)] = append(channels[normalizedReference(channel.Name)], channel)
		}
	}
	for _, prof := range inventory.Profiles {
		seenKeys := map[string]bool{}
		for _, key := range []string{prof.ID, prof.Model, strings.TrimSpace(prof.Vendor + " " + prof.Model)} {
			normalized := normalizedReference(key)
			if key != "" && !seenKeys[normalized] {
				profiles[normalized] = append(profiles[normalized], prof)
				seenKeys[normalized] = true
			}
		}
	}
	rows := []BindingRow{}
	seenDevices := map[string]bool{}
	seenStations := map[string]map[int]bool{}
	for index, record := range records[1:] {
		if blankRecord(record) {
			continue
		}
		row := BindingRow{Line: index + 2, Status: "blocked", Errors: []string{}, Missing: []string{}}
		values := make([]string, 4)
		for i, column := range columns {
			if column < len(record) {
				values[i] = strings.TrimSpace(record[column])
			}
		}
		row.Device, row.Model, row.Channel = values[0], values[1], values[2]
		if device, message := uniqueBinding(devices[normalizedReference(row.Device)], "设备"); message != "" {
			row.Errors = append(row.Errors, message)
		} else {
			row.DeviceID = device.ID
			if device.Bound {
				row.Errors = append(row.Errors, "设备已绑定")
			}
			if seenDevices[device.ID] {
				row.Errors = append(row.Errors, "设备重复")
			}
			seenDevices[device.ID] = true
		}
		var selectedChannel BatchChannel
		if channel, message := uniqueBinding(channels[normalizedReference(row.Channel)], "通道"); message != "" {
			row.Errors = append(row.Errors, message)
		} else {
			selectedChannel = channel
			row.ChannelID = channel.ID
		}
		var selectedProfile profile.Profile
		if prof, message := uniqueBinding(profiles[normalizedReference(row.Model)], "型号"); message != "" {
			row.Errors = append(row.Errors, message)
		} else {
			selectedProfile = prof
			row.ProfileID = prof.ID
			if len(prof.PointSpecs) == 0 {
				row.Errors = append(row.Errors, "型号没有点位")
			}
		}
		station, parseErr := strconv.Atoi(values[3])
		if parseErr != nil || station < 1 || station > 247 {
			row.Errors = append(row.Errors, "站号必须是 1–247 的整数")
		} else {
			row.Station = station
			if row.ChannelID != "" {
				if seenStations[row.ChannelID] == nil {
					seenStations[row.ChannelID] = map[int]bool{}
				}
				if selectedChannel.Occupied[station] || seenStations[row.ChannelID][station] {
					row.Errors = append(row.Errors, "站号冲突")
				}
				seenStations[row.ChannelID][station] = true
			}
		}
		if len(row.Errors) == 0 {
			row.Valid = true
			row.Status = "ready"
			row.CreatedPoints = len(selectedProfile.PointSpecs)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty_or_invalid_csv")
	}
	return rows, nil
}

func bindingColumns(header []string) ([4]int, error) {
	var columns [4]int
	for i := range columns {
		columns[i] = -1
	}
	aliases := [4][]string{{"device", "设备"}, {"model", "profile", "型号"}, {"channel", "通道"}, {"station", "站号"}}
	for index, cell := range header {
		key := normalizedReference(strings.TrimPrefix(cell, string(rune(0xFEFF))))
		for kind, names := range aliases {
			for _, name := range names {
				if key == normalizedReference(name) {
					columns[kind] = index
				}
			}
		}
	}
	for _, column := range columns {
		if column < 0 {
			return columns, fmt.Errorf("invalid_csv_header")
		}
	}
	return columns, nil
}

func normalizedReference(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func blankRecord(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func uniqueBinding[T any](items []T, kind string) (T, string) {
	var zero T
	if len(items) == 0 {
		return zero, kind + "不存在"
	}
	if len(items) > 1 {
		return zero, kind + "名称不唯一"
	}
	return items[0], ""
}
