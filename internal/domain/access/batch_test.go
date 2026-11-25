package access

import (
	"strings"
	"testing"

	"fastiot/internal/ems/profile"
)

func batchFixture() BatchInventory {
	return BatchInventory{
		Devices:  []BatchDevice{{ID: "device-1", Name: "PCS 1"}, {ID: "device-2", Name: "PCS 2"}, {ID: "bound", Name: "已绑定", Bound: true}, {ID: "dup-a", Name: "同名"}, {ID: "dup-b", Name: "同名"}},
		Channels: []BatchChannel{{ID: "channel-1", Name: "总线 A", Occupied: map[int]bool{9: true}}, {ID: "channel-2", Name: "总线 B", Occupied: map[int]bool{}}},
		Profiles: []profile.Profile{{ID: "pcs-v1", Vendor: "厂商", Model: "PCS V1", PointSpecs: []profile.PointSpec{{Name: "功率"}}}, {ID: "bms-v1", Vendor: "厂商", Model: "BMS V1"}},
	}
}

func TestPreflightCSVResolvesRealRowsAndReportsEveryConflict(t *testing.T) {
	t.Parallel()
	text := "设备,型号,通道,站号\nPCS 1,PCS V1,总线 A,10\nPCS 2,pcs-v1,channel-1,10\n已绑定,PCS V1,总线 A,11\nmissing,PCS V1,总线 A,12\n同名,PCS V1,总线 A,13\nPCS 1,PCS V1,总线 A,9\n"
	rows, err := PreflightCSV(text, batchFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	if !rows[0].Valid || rows[0].DeviceID != "device-1" || rows[0].ProfileID != "pcs-v1" || rows[0].ChannelID != "channel-1" || rows[0].Station != 10 || rows[0].CreatedPoints != 1 {
		t.Fatalf("valid row = %+v", rows[0])
	}
	for i, want := range map[int]string{1: "站号冲突", 2: "设备已绑定", 3: "设备不存在", 4: "设备名称不唯一", 5: "站号冲突"} {
		if rows[i].Valid || !strings.Contains(strings.Join(rows[i].Errors, "；"), want) {
			t.Errorf("row %d = %+v, want error %q", i+2, rows[i], want)
		}
	}
}

func TestPreflightCSVRejectsDuplicateDeviceInvalidStationAndUnknownReferences(t *testing.T) {
	t.Parallel()
	text := "device,profile,channel,station\ndevice-1,unknown,channel-1,1\ndevice-1,pcs-v1,missing,1\ndevice-2,pcs-v1,channel-2,248\n"
	rows, err := PreflightCSV(text, batchFixture())
	if err != nil {
		t.Fatal(err)
	}
	for i, wants := range map[int][]string{0: {"型号不存在"}, 1: {"设备重复", "通道不存在"}, 2: {"站号必须是 1–247 的整数"}} {
		for _, want := range wants {
			if !strings.Contains(strings.Join(rows[i].Errors, "；"), want) {
				t.Errorf("row %d errors = %v, want %q", i, rows[i].Errors, want)
			}
		}
	}
}

func TestPreflightCSVRequiresKnownHeaderAndValidCSV(t *testing.T) {
	t.Parallel()
	if _, err := PreflightCSV("name,model,channel,station\n", batchFixture()); err == nil {
		t.Fatal("unknown header accepted")
	}
	if _, err := PreflightCSV("设备,型号,通道,站号\n\"unterminated", batchFixture()); err == nil {
		t.Fatal("invalid CSV accepted")
	}
}

func TestPreflightCSVRejectsProfileWithoutPoints(t *testing.T) {
	t.Parallel()
	rows, err := PreflightCSV("设备,型号,通道,站号\ndevice-2,bms-v1,channel-2,2\n", batchFixture())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Valid || !strings.Contains(strings.Join(rows[0].Errors, "；"), "型号没有点位") {
		t.Fatalf("empty Profile accepted: %+v", rows[0])
	}
}

func TestPreflightCSVDoesNotDuplicateOneProfileWithMatchingIDAndModel(t *testing.T) {
	t.Parallel()
	inventory := batchFixture()
	inventory.Profiles = append(inventory.Profiles, profile.Profile{ID: "same", Model: "same", PointSpecs: []profile.PointSpec{{Name: "功率"}}})
	rows, err := PreflightCSV("设备,型号,通道,站号\ndevice-2,same,channel-2,2\n", inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Valid {
		t.Fatalf("one Profile was treated as ambiguous: %+v", rows[0])
	}
}
