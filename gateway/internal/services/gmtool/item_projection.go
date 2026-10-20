package gmtool

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
)

type itemSection struct {
	Name     string        `json:"name"`
	Label    string        `json:"label"`
	Values   []string      `json:"values,omitempty"`
	Children []itemSection `json:"children,omitempty"`
	tokens   []dnfpvf.Token
}
type itemText struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}
type itemProjection struct {
	Item         map[string]any  `json:"item"`
	Texts        []itemText      `json:"texts"`
	Attributes   []itemAttribute `json:"attributes,omitempty"`
	Sections     []itemSection   `json:"sections"`
	EquipmentSet map[string]any  `json:"equipmentSet,omitempty"`
	PVFChecksum  string          `json:"pvfChecksum"`
}

var itemSectionLabels = map[string]string{
	"name": "名称", "basic explain": "效果说明", "detail explain": "详细效果", "explain": "使用说明", "flavor text": "物品故事",
	"physical attack": "力量", "magical attack": "智力", "physical defense": "体力", "magical defense": "精神",
	"equipment physical attack": "基础物理攻击力", "equipment magical attack": "基础魔法攻击力", "separate attack": "基础独立攻击力",
	"equipment physical defense": "基础物理防御力", "equipment magical defense": "基础魔法防御力",
	"if": "触发条件", "then": "触发效果", "else": "其他条件", "time": "时间条件", "target": "目标",
	"stat change by stat": "属性联动", "stat change": "属性变化", "appendage": "附加效果", "equipment duration": "效果持续时间",
	"all skill item container": "技能加成", "all skill item": "技能规则", "skill apply condition": "技能适用条件", "skill group": "技能范围",
	"lower bound level": "最低技能等级", "upper bound level": "最高技能等级", "extra condition": "附加条件", "skill phase": "技能阶段",
	"item growtype": "适用职业与转职", "value": "数值", "skill levelup": "技能等级", "skill data up": "技能参数",
	"piece set ability": "套装效果", "parameter basic explain": "套装效果说明", "parameter detail explain": "套装详细效果",
	"usable job": "适用职业", "minimum level": "使用等级", "attach type": "绑定类型", "stackable type": "道具类型", "equipment type": "装备类型",
	"usable period": "有效天数", "expiration date": "到期时间", "cool time": "冷却时间（毫秒）", "cool time group": "冷却组", "need material": "所需材料",
	"fatigue value": "疲劳值", "hp max": "生命值上限", "mp max": "魔法值上限", "durability": "耐久度", "weight": "重量（克）",
	"rarity": "稀有度", "grade": "品级", "price": "价格", "repair price": "修理价格", "stack limit": "叠加上限", "part set index": "套装编号",
	"elemental property": "攻击属性", "dark resistance": "暗属性抗性", "fire resistance": "火属性抗性", "light resistance": "光属性抗性", "water resistance": "冰属性抗性",
	"anti evil": "抗魔值", "attack speed": "攻击速度参数", "cast speed": "施放速度参数", "move speed": "移动速度参数",
}

func sectionKeyDetail(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// Keep explicit block boundaries and repeated sections. Conditional values must
// never be flattened into the unconditional equipment-stat projection.
func itemSections(doc *dnfpvf.Document) ([]itemSection, *dnfpvf.Document) {
	pairs := map[int]int{}
	pending := map[string][]int{}
	for i, section := range doc.Sections {
		name := sectionKeyDetail(section.Name)
		if strings.HasPrefix(name, "/") {
			key := strings.TrimPrefix(name, "/")
			stack := pending[key]
			if len(stack) > 0 {
				j := stack[len(stack)-1]
				pairs[j] = i
				pending[key] = stack[:len(stack)-1]
			}
		} else {
			pending[name] = append(pending[name], i)
		}
	}
	root := &dnfpvf.Document{Path: doc.Path}
	var collect func(int, int, int) []itemSection
	collect = func(start, end, depth int) []itemSection {
		out := make([]itemSection, 0)
		for i := start; i < end; i++ {
			section := doc.Sections[i]
			name := sectionKeyDetail(section.Name)
			node := itemSection{Name: name, Label: itemSectionLabels[name]}
			if node.Label == "" {
				node.Label = "[" + name + "]"
			}
			tokens := documentSectionTokens(doc, section)
			node.tokens = tokens
			line := -1
			for _, token := range tokens {
				value := token.Value
				if token.Kind == dnfpvf.TokenInt || token.Kind == dnfpvf.TokenFloat {
					value = token.Raw
				}
				if len(node.Values) == 0 || token.Line != line {
					node.Values = append(node.Values, value)
				} else {
					node.Values[len(node.Values)-1] += "  " + value
				}
				line = token.Line
			}
			if depth == 0 && !strings.HasPrefix(name, "/") {
				pos := len(root.Tokens)
				root.Tokens = append(root.Tokens, tokens...)
				root.Sections = append(root.Sections, dnfpvf.Section{Name: section.Name, Start: pos, End: len(root.Tokens)})
			}
			if close, ok := pairs[i]; ok && close < end && depth < 64 {
				node.Children = collect(i+1, close, depth+1)
				i = close
			}
			out = append(out, node)
		}
		return out
	}
	return collect(0, len(doc.Sections), 0), root
}

func (s *Server) projectItem(ctx context.Context, source dnfpvf.Source, entry itemEntry) (*itemProjection, error) {
	doc, err := itemDocument(ctx, source, entry)
	if err != nil {
		return nil, err
	}
	sections, root := itemSections(doc)
	item := itemSummaryPayload(entry, root)
	item["itemId"] = strconv.FormatUint(uint64(entry.ID), 10)
	jobs := strings.Join(root.Texts("usable job"), " ")
	item["usableJob"] = jobs
	item["usableJobs"] = usableJobLabels(jobs)
	result := &itemProjection{Item: item, Sections: sections, Texts: []itemText{}, PVFChecksum: itemIndexSourceChecksum(source)}
	seen := map[string]bool{}
	for _, name := range []string{"explain", "basic explain", "detail explain", "flavor text"} {
		value := itemDetailText(root, name)
		if value != "" && !seen[value] {
			result.Texts = append(result.Texts, itemText{Title: itemSectionLabels[name], Text: value})
			seen[value] = true
		}
	}
	if entry.Kind == "equipment" {
		result.Attributes = equipmentDetailAttributes(root)
		if setID, ok := root.Int("part set index"); ok && setID > 0 {
			result.EquipmentSet, err = s.partSetDetail(ctx, setID)
			if err != nil {
				return nil, err
			}
			if result.EquipmentSet != nil {
				text, e := source.ReadText(result.EquipmentSet["pvfPath"].(string))
				if e != nil {
					return nil, e
				}
				setDoc, e := dnfpvf.Parse(result.EquipmentSet["pvfPath"].(string), text)
				if e != nil {
					return nil, e
				}
				blocks, _ := itemSections(setDoc)
				result.EquipmentSet["sections"] = blocks
				result.EquipmentSet["bonuses"] = projectedSetBonuses(blocks)
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func projectedSetBonuses(blocks []itemSection) []equipmentSetBonus {
	bonuses := []equipmentSetBonus{}
	for _, block := range blocks {
		if block.Name != "piece set ability" || len(block.tokens) == 0 || block.tokens[0].Kind != dnfpvf.TokenInt || block.tokens[0].Int <= 0 {
			continue
		}
		root := &dnfpvf.Document{}
		for _, child := range block.Children {
			pos := len(root.Tokens)
			root.Tokens = append(root.Tokens, child.tokens...)
			root.Sections = append(root.Sections, dnfpvf.Section{Name: child.Name, Start: pos, End: len(root.Tokens)})
		}
		bonuses = append(bonuses, equipmentSetBonus{RequiredPieces: int(block.tokens[0].Int), BasicExplain: itemDetailText(root, "parameter basic explain"), DetailExplain: itemDetailText(root, "parameter detail explain"), Attributes: equipmentSetAttributes(root)})
	}
	return bonuses
}

func (s *Server) v1ItemDetail(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("itemId")
	id, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != rawID || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "物品编号或查询参数无效", false)
		return
	}
	if s.items == nil || !s.items.isReady() || s.items.failure() != nil {
		v1Fail(w, r, 503, "item_index_unavailable", "物品目录尚未准备好，请稍后刷新", true)
		return
	}
	entry, ok := s.items.lookup(uint32(id))
	if !ok {
		v1Fail(w, r, 404, "item_not_found", "物品不在当前目录中", false)
		return
	}
	source, err := s.source()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	var result *itemProjection
	if mode, ok := source.(pvfModeControl); ok && mode.ModeStatus().Mode == "memory" {
		result, err = s.projectItem(r.Context(), source, entry)
	} else {
		result, err = readItemDetailIndex(r.Context(), itemDetailIndexPath(s.items.cachePath, itemIndexSourceChecksum(source)), itemIndexSourceChecksum(source), entry.ID)
	}
	if err != nil {
		result, err = s.projectItem(r.Context(), source, entry)
	}
	if err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger("item detail unavailable", "itemId", entry.ID, "error", err)
		}
		v1Fail(w, r, 503, "item_detail_unavailable", "物品详情读取失败，请重试", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: result})
}

func projectionItemID(value *itemProjection) (uint32, error) {
	id, err := strconv.ParseUint(fmt.Sprint(value.Item["itemId"]), 10, 32)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid projected item ID")
	}
	return uint32(id), nil
}
