package deepseek

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

// PointSummary is the device-model structure the model may see: a point name,
// its kind and its unit. Live values, timestamps, origins and register
// addresses are deliberately absent.
type PointSummary struct {
	// ID is the opaque node ID the generated markup binds to. It is not an
	// address, credential or live value.
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
	Unit string `json:"unit,omitempty"`
}

// Screenshot is an uploaded screenshot with the regions the deliverer marked
// on it. Data is the local base64 image; it is measured and inspected but
// never sent to the model.
type Screenshot struct {
	Name       string   `json:"name"`
	MediaType  string   `json:"media_type"`
	Data       string   `json:"data,omitempty"`
	Annotation string   `json:"annotation,omitempty"`
	Regions    []Region `json:"regions,omitempty"`
	Reviewed   bool     `json:"reviewed"`
}

// Region is one user-marked area of a screenshot. X and Y are relative to the
// image (0..1); only the coordinates and the note leave the process.
type Region struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Note string  `json:"note"`
}

const (
	// ModeRevise modifies the screen already in the preview session instead of
	// generating a new one. An empty mode is first generation.
	ModeRevise = "revise"

	maxRegionsPerScreenshot = 20
)

// Request is one screen-generation request before anything leaves the
// process. Acknowledged carries finding IDs the deliverer explicitly accepted.
type Request struct {
	Mode         string         `json:"mode,omitempty"`
	Instruction  string         `json:"instruction"`
	Context      string         `json:"context,omitempty"`
	Points       []PointSummary `json:"points,omitempty"`
	Screenshots  []Screenshot   `json:"screenshots,omitempty"`
	Acknowledged []string       `json:"acknowledged,omitempty"`
}

type Category struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
}

type CodeSection struct {
	Instruction string `json:"instruction"`
	Context     string `json:"context,omitempty"`
}

type ScreenshotSummary struct {
	Name        string   `json:"name"`
	MediaType   string   `json:"media_type"`
	Bytes       int      `json:"bytes"`
	Width       int      `json:"width,omitempty"`
	Height      int      `json:"height,omitempty"`
	Annotation  string   `json:"annotation,omitempty"`
	Regions     []Region `json:"regions,omitempty"`
	Reviewed    bool     `json:"reviewed"`
	SendsPixels bool     `json:"sends_pixels"`
	Invalid     bool     `json:"invalid,omitempty"`
}

type Finding struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Item     int    `json:"item"`
	Field    string `json:"field"`
	Kind     string `json:"kind"`
	Position int    `json:"position"`
	Line     int    `json:"line"`
	Preview  string `json:"preview"`
	Blocking bool   `json:"blocking"`
}

// Manifest is the exact outbound list shown before generation. Categories are
// the three content classes; findings locate suspected sensitive content.
type Manifest struct {
	Categories   []Category          `json:"categories"`
	Mode         string              `json:"mode,omitempty"`
	Code         CodeSection         `json:"code"`
	DeviceModel  []PointSummary      `json:"device_model"`
	Screenshots  []ScreenshotSummary `json:"screenshots"`
	Findings     []Finding           `json:"findings"`
	BlockReasons []string            `json:"block_reasons,omitempty"`
	Blocked      bool                `json:"blocked"`
}

// BuildManifest derives both the preview and the payload from one request, so
// what the deliverer reviews is what the model receives.
func BuildManifest(req Request) Manifest {
	instruction := strings.TrimSpace(req.Instruction)
	manifest := Manifest{
		Mode:        req.Mode,
		Code:        CodeSection{Instruction: instruction, Context: req.Context},
		DeviceModel: append([]PointSummary{}, req.Points...),
		Screenshots: []ScreenshotSummary{},
		Findings:    []Finding{},
	}
	regionCount := 0
	for _, screenshot := range req.Screenshots {
		regionCount += len(screenshot.Regions)
	}
	manifest.Categories = []Category{
		{Key: "code", Label: "代码与上下文", Summary: fmt.Sprintf("指令 %d 字；现有画面配置 %d 字", utf8.RuneCountInString(instruction), utf8.RuneCountInString(req.Context))},
		{Key: "device_model", Label: "设备模型摘要", Summary: fmt.Sprintf("%d 个点位，仅名称、类型、单位与点位 id，不含实时值与寄存器地址", len(req.Points))},
		{Key: "screenshots", Label: "截图", Summary: fmt.Sprintf("%d 张截图，%d 个标注区域，仅文件名、标注文字与区域坐标，不发送图片像素", len(req.Screenshots), regionCount)},
	}

	manifest.Findings = append(manifest.Findings, ScanText("code", 0, "instruction", req.Instruction)...)
	if req.Context != "" {
		manifest.Findings = append(manifest.Findings, ScanText("code", 0, "context", req.Context)...)
	}
	for index, point := range req.Points {
		manifest.Findings = append(manifest.Findings, ScanText("device_model", index, "name", point.Name)...)
		if point.Unit != "" {
			manifest.Findings = append(manifest.Findings, ScanText("device_model", index, "unit", point.Unit)...)
		}
	}

	unreviewed := 0
	invalid := 0
	annotationInvalid := 0
	for index, screenshot := range req.Screenshots {
		summary := ScreenshotSummary{
			Name: screenshot.Name, MediaType: screenshot.MediaType,
			Annotation: screenshot.Annotation, Reviewed: screenshot.Reviewed,
		}
		manifest.Findings = append(manifest.Findings, ScanText("screenshot", index, "name", screenshot.Name)...)
		if screenshot.Annotation != "" {
			manifest.Findings = append(manifest.Findings, ScanText("screenshot", index, "annotation", screenshot.Annotation)...)
		}
		if len(screenshot.Regions) > maxRegionsPerScreenshot {
			summary.Invalid = true
			annotationInvalid++
		} else {
			regions := make([]Region, 0, len(screenshot.Regions))
			for _, region := range screenshot.Regions {
				note := strings.TrimSpace(region.Note)
				if region.X < 0 || region.X > 1 || region.Y < 0 || region.Y > 1 || note == "" {
					summary.Invalid = true
					annotationInvalid++
					continue
				}
				regions = append(regions, Region{X: region.X, Y: region.Y, Note: note})
				manifest.Findings = append(manifest.Findings, ScanText("screenshot", index, "region", note)...)
			}
			summary.Regions = regions
		}
		if screenshot.Data != "" {
			data, err := decodeImageData(screenshot.Data)
			if err != nil {
				summary.Invalid = true
				invalid++
			} else {
				summary.Bytes = len(data)
				width, height, texts := pngTextChunks(data)
				summary.Width, summary.Height = width, height
				for _, text := range texts {
					manifest.Findings = append(manifest.Findings, ScanText("screenshot", index, "metadata", text)...)
				}
			}
		}
		if !screenshot.Reviewed {
			unreviewed++
		}
		manifest.Screenshots = append(manifest.Screenshots, summary)
	}

	acknowledged := map[string]bool{}
	for _, id := range req.Acknowledged {
		acknowledged[id] = true
	}
	pending := 0
	for index := range manifest.Findings {
		manifest.Findings[index].Blocking = !acknowledged[manifest.Findings[index].ID]
		if manifest.Findings[index].Blocking {
			pending++
		}
	}

	reasons := []string{}
	if instruction == "" {
		reasons = append(reasons, "instruction_required")
	}
	if pending > 0 {
		reasons = append(reasons, "sensitive_content")
	}
	if invalid > 0 {
		reasons = append(reasons, "screenshot_invalid")
	}
	if annotationInvalid > 0 {
		reasons = append(reasons, "screenshot_annotation_invalid")
	}
	if unreviewed > 0 {
		reasons = append(reasons, "screenshot_unreviewed")
	}
	manifest.BlockReasons = reasons
	manifest.Blocked = len(reasons) > 0
	return manifest
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const reviseInstruction = "你在修改一个已经生成的画面。请只做用户要求的修改，保留未要求改动的内容，输出修改后的完整 HTML 文档，不要输出解释文字。\n"

// MessagesForManifest renders the model request from the reviewed manifest.
// Only the fields shown in the manifest are included.
func MessagesForManifest(manifest Manifest) []Message {
	const system = "你是工业组态画面生成助手。只使用下方给出的设备模型，不虚构点位、单位或数值；" +
		"只输出一个完整的 HTML 文档，不要输出解释文字。\n" +
		"绑定规则（预览与发布都按此执行）：\n" +
		"- 读取点位：在元素上写 data-fastiot-point=\"<点位 ID>\"，可用 data-fastiot-format=\"number|text\" 与 data-fastiot-decimals=\"1\" 控制显示。\n" +
		"- 写入点位：只在 <button> 上写 data-fastiot-write=\"<点位 ID>\" 和 data-fastiot-value=\"<数值>\"。\n" +
		"- 画面运行在离线隔离沙箱中：不能访问网络、外部脚本/字体/图片 URL、localStorage、cookie、window.parent 或 eval；" +
		"不要使用 onclick 等内联事件属性，交互用上述 data 属性或在 <script> 中 addEventListener。\n" +
		"- 三维画面：平台内置 Three.js（全局 THREE，随平台离线打包）可直接使用；" +
		"用 <canvas> 与 THREE.WebGLRenderer 表达设备空间位置、管路连接和状态（连接关系用 THREE.Line/LineSegments，状态用颜色或发光）。" +
		"不要引用任何外部模型、贴图、字体或 CDN；贴图只能用 data: URI 或由 canvas 程序化生成。" +
		"三维代码读取实时值监听 document 的 'fastiot:values' 事件（event.detail.values['<点位 ID>'].value）。\n" +
		"- 样式写在 <style> 或 style 属性中；图片只能用 data: URI。"
	systemMessage := system
	if manifest.Mode == ModeRevise {
		systemMessage = reviseInstruction + system
	}
	var user strings.Builder
	user.WriteString("用户要求：\n")
	user.WriteString(manifest.Code.Instruction)
	user.WriteString("\n")
	if manifest.Mode == ModeRevise {
		user.WriteString("\n现有画面 HTML（在此基础上修改，输出完整文档）：\n")
		user.WriteString(manifest.Code.Context)
		user.WriteString("\n")
	} else if manifest.Code.Context != "" {
		user.WriteString("\n现有画面配置：\n")
		user.WriteString(manifest.Code.Context)
		user.WriteString("\n")
	}
	user.WriteString("\n设备模型（点位 id 用于绑定；不含实时值、历史与寄存器地址）：\n")
	if len(manifest.DeviceModel) == 0 {
		user.WriteString("（无点位）\n")
	}
	for _, point := range manifest.DeviceModel {
		if point.Unit != "" {
			fmt.Fprintf(&user, "- %s | %s | %s | id=%s\n", point.Name, point.Type, point.Unit, point.ID)
		} else {
			fmt.Fprintf(&user, "- %s | %s | id=%s\n", point.Name, point.Type, point.ID)
		}
	}
	if len(manifest.Screenshots) > 0 {
		user.WriteString("\n标注截图（像素未发送，只有文件名、标注文字与区域坐标）：\n")
		for _, screenshot := range manifest.Screenshots {
			fmt.Fprintf(&user, "- %s（%dx%d）：%s\n", screenshot.Name, screenshot.Width, screenshot.Height, screenshot.Annotation)
			for index, region := range screenshot.Regions {
				fmt.Fprintf(&user, "  标注区域 %d（%.0f%%，%.0f%%）：%s\n", index+1, region.X*100, region.Y*100, region.Note)
			}
		}
	}
	return []Message{{Role: "system", Content: systemMessage}, {Role: "user", Content: user.String()}}
}

func decodeImageData(encoded string) ([]byte, error) {
	if comma := strings.Index(encoded, "base64,"); comma >= 0 {
		encoded = encoded[comma+len("base64,"):]
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// pngTextChunks reads the IHDR size and any uncompressed textual chunks. It
// ignores malformed data instead of guessing.
func pngTextChunks(data []byte) (int, int, []string) {
	signature := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	if len(data) < len(signature) {
		return 0, 0, nil
	}
	for index := range signature {
		if data[index] != signature[index] {
			return 0, 0, nil
		}
	}
	var width, height int
	var texts []string
	offset := len(signature)
	for offset+8 <= len(data) {
		length := int(uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3]))
		if length < 0 || offset+12+length > len(data) {
			break
		}
		kind := string(data[offset+4 : offset+8])
		payload := data[offset+8 : offset+8+length]
		switch kind {
		case "IHDR":
			if len(payload) >= 8 {
				width = int(uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3]))
				height = int(uint32(payload[4])<<24 | uint32(payload[5])<<16 | uint32(payload[6])<<8 | uint32(payload[7]))
			}
		case "tEXt":
			if split := indexByte(payload, 0); split >= 0 {
				texts = append(texts, string(payload[split+1:]))
			}
		case "iTXt":
			// keyword\0 compressionFlag compressionMethod language\0 translated\0 text
			if split := indexByte(payload, 0); split >= 0 && len(payload) > split+3 {
				compressed := payload[split+1]
				rest := payload[split+3:]
				if compressed == 0 {
					if _, tail, ok := splitNul(rest); ok {
						if _, text, ok := splitNul(tail); ok {
							texts = append(texts, string(text))
						}
					}
				}
			}
		}
		offset += 12 + length
	}
	return width, height, texts
}

func indexByte(data []byte, value byte) int {
	for index, item := range data {
		if item == value {
			return index
		}
	}
	return -1
}

func splitNul(data []byte) ([]byte, []byte, bool) {
	if split := indexByte(data, 0); split >= 0 {
		return data[:split], data[split+1:], true
	}
	return nil, nil, false
}
