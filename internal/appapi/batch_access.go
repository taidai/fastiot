package appapi

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/pointconfig"
	"fastiot/internal/ems/profile"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// Import bounds are separate from the legacy parser contract. A rejected CSV
// never becomes a truncated batch with silently omitted devices or fields.
func validateReviewedBatchCSV(text string) error {
	if len(text) > 256*1024 {
		return fmt.Errorf("batch_csv_too_large")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, string(rune(0xFEFF)))))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = 4
	count := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid_csv")
		}
		for _, field := range record {
			if len(field) > 256 {
				return fmt.Errorf("batch_csv_field_too_large")
			}
		}
		if count == 0 {
			aliases := map[string]string{"device": "device", "设备": "device", "model": "model", "profile": "model", "型号": "model", "channel": "channel", "通道": "channel", "station": "station", "站号": "station"}
			seen := map[string]bool{}
			for _, field := range record {
				key := aliases[strings.ToLower(strings.TrimSpace(field))]
				if key == "" || seen[key] {
					return fmt.Errorf("invalid_csv_header")
				}
				seen[key] = true
			}
		}
		count++
		if count > 257 {
			return fmt.Errorf("batch_csv_too_many_rows")
		}
	}
	if count < 2 {
		return fmt.Errorf("empty_or_invalid_csv")
	}
	return nil
}

// A visible Device may already be attached through a channel outside the
// caller's scope. Expose only its occupied flag, never the hidden binding.
func (h *Handler) markBatchBoundDevices(inventory *domainaccess.BatchInventory) error {
	channels, err := client.GetNodes(h.nc, "all", "all", data.NodeTypeModbus, false)
	if err != nil {
		return err
	}
	bound := map[string]bool{}
	for _, channel := range channels {
		for _, point := range channel.Points {
			if point.Type != unitModelPoint || point.Tombstone%2 != 0 {
				continue
			}
			server, err := strconv.Atoi(point.Key)
			if err != nil {
				continue
			}
			if model, ok := unitModelFor(channel, server); ok {
				bound[model.BusinessNodeID] = true
			}
		}
	}
	for i := range inventory.Devices {
		inventory.Devices[i].Bound = bound[inventory.Devices[i].ID]
	}
	return nil
}

type batchProfilePlan struct {
	Match           profileApplyResult `json:"match"`
	CreatePoints    []ModbusIOInput    `json:"create_points"`
	MissingRequired []string           `json:"missing_required"`
}

// Every duplicate is blocked, rather than selecting the first CSV row as a
// winner. Plans use the same Profile matching and point factory as single access.
func (h *Handler) reviewedBatchPlans(rows []domainaccess.BindingRow, profiles []profile.Profile) map[int]batchProfilePlan {
	devices, stations := map[string]int{}, map[string]int{}
	for _, row := range rows {
		if row.DeviceID != "" {
			devices[row.DeviceID]++
		}
		if row.ChannelID != "" && row.Station > 0 {
			stations[fmt.Sprintf("%s/%d", row.ChannelID, row.Station)]++
		}
	}
	plans := map[int]batchProfilePlan{}
	for i := range rows {
		row := &rows[i]
		block := func(reason string) {
			for _, existing := range row.Errors {
				if existing == reason {
					return
				}
			}
			row.Errors = append(row.Errors, reason)
			row.Valid, row.Status = false, "blocked"
		}
		if devices[row.DeviceID] > 1 {
			block("设备重复")
		}
		if stations[fmt.Sprintf("%s/%d", row.ChannelID, row.Station)] > 1 {
			block("站号冲突")
		}
		if !row.Valid {
			continue
		}
		var prof profile.Profile
		for _, candidate := range profiles {
			if candidate.ID == row.ProfileID {
				prof = candidate
				break
			}
		}
		plan := batchProfilePlan{Match: applyProfile(row.Station, prof, h.unitPoints(row.ChannelID), unitModelConfig{}), CreatePoints: profilePointInputs(row.ChannelID, row.Station, prof), MissingRequired: []string{}}
		for _, spec := range domainaccess.RequiredPointSpecs(prof) {
			if plan.Match.Model.PointMap[spec.Name] == "" {
				plan.MissingRequired = append(plan.MissingRequired, spec.Name)
			}
		}
		names := map[string]bool{}
		for _, point := range plan.CreatePoints {
			name := normalizePointName(point.Description)
			if name == "" || names[name] {
				block("Profile 点位名称为空或重复")
			}
			names[name] = true
			if err := pointconfig.Validate(point); err != nil {
				block("Profile 点位无效：" + point.Description + "：" + err.Error())
			}
		}
		plans[row.Line] = plan
	}
	return plans
}

// The dedicated reviewed route fences configuration, not volatile readings. The
// legacy /access/batch route intentionally does not promise a reviewed version.
func (h *Handler) batchPreviewVersion(csv string, identity Identity, inventory domainaccess.BatchInventory) (string, error) {
	sort.Slice(inventory.Devices, func(i, j int) bool { return inventory.Devices[i].ID < inventory.Devices[j].ID })
	sort.Slice(inventory.Channels, func(i, j int) bool { return inventory.Channels[i].ID < inventory.Channels[j].ID })
	sort.Slice(inventory.Profiles, func(i, j int) bool { return inventory.Profiles[i].ID < inventory.Profiles[j].ID })
	transport := map[string]string{}
	points := map[string][]unitModelPointView{}
	models := map[string]string{}
	for _, channel := range inventory.Channels {
		nodes, err := client.GetNodes(h.nc, "all", channel.ID, "", false)
		if err != nil || len(nodes) != 1 {
			return "", fmt.Errorf("channel_unavailable")
		}
		transport[channel.ID] = bridge.AcquisitionStamp(nodes[0], data.NodeEdge{})
		points[channel.ID] = h.unitPoints(channel.ID)
		sort.Slice(points[channel.ID], func(i, j int) bool { return points[channel.ID][i].ID < points[channel.ID][j].ID })
		for _, point := range nodes[0].Points {
			if point.Type == unitModelPoint && point.Tombstone%2 == 0 {
				models[channel.ID+"/"+point.Key] = point.Txt()
			}
		}
	}
	return accessHash(struct {
		CSV, Actor string
		Inventory  domainaccess.BatchInventory
		Transport  map[string]string
		Points     map[string][]unitModelPointView
		Models     map[string]string
	}{csv, identity.UserID, inventory, transport, points, models}), nil
}
