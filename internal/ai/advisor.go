package ai

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ---- A: second opinion on an augment choice ----

// Card is one augment on screen, with what the statistics say about it.
type Card struct {
	Name    string   `json:"name"`
	Rarity  string   `json:"rarity"`
	Desc    string   `json:"desc"`
	Reasons []string `json:"reasons"` // the data's own reasons (win rate, pairs, item fit, route)
}

// Player is a champion in the game with its finished items.
type Player struct {
	Champion string   `json:"champion"`
	Items    []string `json:"items,omitempty"`
}

// ReviewInput is everything the model sees for one augment choice.
type ReviewInput struct {
	Champion   string   `json:"champion"`
	Level      int      `json:"level"`
	Stage      string   `json:"stage"` // 开局 / 7 级 / 11 级 / 15 级
	Picked     []string `json:"picked"`
	MyItems    []string `json:"myItems"`
	Allies     []Player `json:"allies"`
	Enemies    []Player `json:"enemies"`
	Cards      []Card   `json:"cards"`    // statistics' order: best first
	DataPick   string   `json:"dataPick"` // what the statistics recommend
	Strategy   string   `json:"strategy"` // the pre-game strategy card, if any
	GameMinute int      `json:"gameMinute"`
}

// Review is the model's verdict.
type Review struct {
	Pick       string  `json:"pick"`
	Reason     string  `json:"reason"`
	Agree      bool    `json:"agree"`
	Confidence string  `json:"confidence"`
	Seconds    float64 `json:"seconds"`
}

const reviewSystem = `你是英雄联盟「海克斯乱斗」（ARAM Mayhem）的强化选择顾问，给玩家第二意见。
规则：
- 只能从给出的选项里选一个，pick 必须和选项名字一字不差。
- 统计数据（胜率、场次、组合、装备契合、路线）来自上千万场真实对局，是主要依据；你的价值在于补充数据看不到的：敌我阵容（比如对面多坦克、多回复、多控制）、已选强化和英雄技能的机制配合、当前装备路线、本局策略。
- 引用数字时只能用给出的数字，不能编造。
- 逐个检查这个英雄的技能和普攻能不能触发强化的效果（比如要求技能命中英雄、攻击特效、持续伤害、暴击、护盾、治疗、召唤物等）。触发不了或几乎触发不了的强化是「坑」，不要选它，并在 reason 里点明；数据里标了「坑」的同样别选。
- 如果数据推荐没有明显问题，就同意它（agree=true）；只有阵容或机制上有明确理由时才改选。
- reason 用中文，不超过 60 字，直接说为什么。
只输出 JSON，格式：
{"pick": "强化名", "reason": "理由", "agree": true, "confidence": "高"}
confidence 只能是 高、中、低。`

// ReviewChoice asks for a second opinion on one augment choice. Real-time:
// no thinking mode, small output.
func (c *Client) ReviewChoice(ctx context.Context, in ReviewInput) (*Review, error) {
	t0 := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "英雄：%s，%d 级，第 %d 分钟，这次是%s强化。\n", in.Champion, in.Level, in.GameMinute, in.Stage)
	if len(in.Picked) > 0 {
		fmt.Fprintf(&b, "已选强化：%s\n", strings.Join(in.Picked, "、"))
	}
	fmt.Fprintf(&b, "我的装备：%s\n", orNone(in.MyItems))
	writeTeam(&b, "我方", in.Allies)
	writeTeam(&b, "敌方", in.Enemies)
	if in.Strategy != "" {
		fmt.Fprintf(&b, "本局策略卡：%s\n", in.Strategy)
	}
	b.WriteString("\n三个选项（按统计数据从好到差）：\n")
	for i, cd := range in.Cards {
		fmt.Fprintf(&b, "%d. %s（%s）\n   效果：%s\n   数据：%s\n", i+1, cd.Name, cd.Rarity, cd.Desc, strings.Join(cd.Reasons, "；"))
	}
	fmt.Fprintf(&b, "\n统计数据推荐：%s。请给出你的选择，输出 json。", in.DataPick)
	var r Review
	err := c.ChatJSON(ctx, []Message{{Role: "system", Content: reviewSystem}, {Role: "user", Content: b.String()}}, ChatOptions{MaxTokens: 400}, &r)
	if err != nil {
		return nil, err
	}
	r.Seconds = time.Since(t0).Seconds()
	// The pick must be one of the options.
	ok := false
	for _, cd := range in.Cards {
		if cd.Name == strings.TrimSpace(r.Pick) {
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("AI 选了不在选项里的「%s」", r.Pick)
	}
	// "Agrees" means it picked the same as the statistics, whatever it said.
	r.Pick = strings.TrimSpace(r.Pick)
	r.Agree = r.Pick == in.DataPick
	return &r, nil
}

func writeTeam(b *strings.Builder, label string, ps []Player) {
	if len(ps) == 0 {
		return
	}
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		if len(p.Items) > 0 {
			parts = append(parts, p.Champion+"（"+strings.Join(p.Items, "、")+"）")
		} else {
			parts = append(parts, p.Champion)
		}
	}
	fmt.Fprintf(b, "%s：%s\n", label, strings.Join(parts, "；"))
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "暂无成装"
	}
	return strings.Join(xs, "、")
}

// ---- B: pre-game research on the web ----

// AugStat is an augment with its statistics for the champion.
type AugStat struct {
	Name    string  `json:"name"`
	Rarity  string  `json:"rarity"`
	WinRate float64 `json:"winRate"`
	Games   int     `json:"games"`
}

// ResearchInput is what we already know about the champion.
type ResearchInput struct {
	Champion   string // display name (SEA Chinese)
	ChampionEN string // English key, for English sources
	Patch      string
	WinRate    float64
	Augments   []AugStat // best first; names the card may use
	Cores      []string  // popular core builds, as text
}

// Strategy is the pre-game strategy card.
type Strategy struct {
	Summary  string        `json:"summary"`
	Prefer   []StrategyAug `json:"prefer"`
	Avoid    []StrategyAug `json:"avoid"`
	Build    []string      `json:"build"`
	Sources  []string      `json:"sources"`
	Searched []string      `json:"searched"` // queries used
	At       time.Time     `json:"at"`
}

// StrategyAug is an augment the card recommends or warns against.
type StrategyAug struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// Text renders the card in one paragraph (fed to the real-time review).
func (s *Strategy) Text() string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(s.Summary)
	if len(s.Prefer) > 0 {
		b.WriteString(" 优先：")
		for i, a := range s.Prefer {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(a.Name)
		}
		b.WriteString("。")
	}
	if len(s.Avoid) > 0 {
		b.WriteString(" 避免：")
		for i, a := range s.Avoid {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(a.Name)
		}
		b.WriteString("。")
	}
	return b.String()
}

const researchSystem = `你是英雄联盟「海克斯乱斗」（ARAM Mayhem）攻略研究员。根据给出的网络搜索结果和统计数据，为一个英雄写一张「本局策略卡」。
规则：
- 强化名字只能用「可用强化」列表里的名字，一字不差；搜索结果里叫法不同的，换成列表里对应的名字，对不上就不要写。
- 统计数据来自上千万场对局，和搜索结果冲突时以统计数据为准，但可以说明攻略里提到的打法和配合。
- 不要编造数字。
- avoid 优先写这个英雄的技能机制触发不了效果的强化（比如要求技能命中、攻击特效、持续伤害、暴击的强化，而这个英雄做不到），why 里说清楚为什么触发不了。
- 全部用中文，简洁。
只输出 JSON，格式：
{"summary": "不超过 80 字的整体思路", "prefer": [{"name": "强化名", "why": "不超过 30 字"}], "avoid": [{"name": "强化名", "why": "不超过 30 字"}], "build": ["不超过 30 字的出装要点"], "sources": ["用到的网址"]}
prefer 最多 6 个，avoid 最多 3 个，build 最多 3 条。`

// Research searches the web for the champion in ARAM Mayhem and writes a strategy card.
func (c *Client) Research(ctx context.Context, in ResearchInput) (*Strategy, error) {
	queries := []string{
		in.Champion + " 海克斯大乱斗 强化 出装 lang:zh",
		in.ChampionEN + " ARAM Mayhem augments build lang:en",
	}
	var b strings.Builder
	seen := map[string]bool{}
	n := 0
	for _, q := range queries {
		res, err := c.Search(ctx, q, 6)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			if seen[r.URL] || r.Summary == "" {
				continue
			}
			seen[r.URL] = true
			n++
			fmt.Fprintf(&b, "[%d] %s\n%s\n%s\n\n", n, r.Title, r.URL, r.Summary)
		}
	}
	var u strings.Builder
	fmt.Fprintf(&u, "英雄：%s（%s），版本 %s，海克斯乱斗胜率 %.1f%%。\n\n", in.Champion, in.ChampionEN, in.Patch, in.WinRate*100)
	u.WriteString("可用强化（统计数据，按这个英雄的胜率从高到低）：\n")
	for _, a := range in.Augments {
		fmt.Fprintf(&u, "- %s（%s）胜率 %.1f%%，%d 场\n", a.Name, a.Rarity, a.WinRate*100, a.Games)
	}
	if len(in.Cores) > 0 {
		u.WriteString("\n常见核心出装（统计数据）：\n")
		for _, s := range in.Cores {
			u.WriteString("- " + s + "\n")
		}
	}
	if n == 0 {
		u.WriteString("\n（没有搜到相关网页，只根据统计数据写。）\n")
	} else {
		u.WriteString("\n网络搜索结果：\n" + b.String())
	}
	u.WriteString("\n请写策略卡，输出 json。")
	var s Strategy
	if err := c.ChatJSON(ctx, []Message{{Role: "system", Content: researchSystem}, {Role: "user", Content: u.String()}},
		ChatOptions{Think: true, Effort: "low", MaxTokens: 4000}, &s); err != nil {
		return nil, err
	}
	// Keep only augment names we know.
	known := map[string]bool{}
	for _, a := range in.Augments {
		known[a.Name] = true
	}
	filter := func(xs []StrategyAug) []StrategyAug {
		var out []StrategyAug
		for _, x := range xs {
			if known[strings.TrimSpace(x.Name)] {
				out = append(out, x)
			}
		}
		return out
	}
	s.Prefer, s.Avoid = filter(s.Prefer), filter(s.Avoid)
	s.Searched = queries
	s.At = time.Now()
	return &s, nil
}

// ---- C: read augment cards from garbled OCR text ----

// OCRLine is a line the OCR read in the middle of the screen, with its
// position (0..1, left to right / top to bottom).
type OCRLine struct {
	Text string  `json:"text"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// ScreenRead is the model's reading of the OCR text.
type ScreenRead struct {
	Choosing bool     `json:"choosing"`
	Names    []string `json:"names"` // left to right; "" where a card could not be mapped
}

const ocrPrompt = `下面是文字识别（OCR）从英雄联盟「海克斯乱斗」游戏画面中间读到的文字，可能有错字、漏字、多余空格或把一行拆成几段。每行带位置：x 从左到右 0~1，y 从上到下 0~1。
任务：判断画面上是不是正在「选择强化」——通常是并排 2 到 4 张卡片，卡片标题（强化名字）大致在同一行（y 接近），x 分得开，标题下面是效果描述。
如果是，按 x 从左到右，把每张卡片的标题对应到「候选强化」里的一个名字（必须一字不差地用候选里的名字）；某张卡对不上就填空字符串 ""。不要因为描述里提到某个词就当成标题。
如果不是在选强化，choosing 填 false，names 为空。
只输出 JSON，格式：{"choosing": true, "names": ["候选名1", "候选名2", "候选名3"]}`

// ReadOCR asks the model which augments the (possibly garbled) OCR lines
// show. Text only; candidates should be a short list of plausible names.
func (c *Client) ReadOCR(ctx context.Context, lines []OCRLine, candidates []string) (*ScreenRead, error) {
	var b strings.Builder
	b.WriteString(ocrPrompt)
	b.WriteString("\n\nOCR 读到的文字：\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "- (x=%.2f, y=%.2f) %s\n", l.X, l.Y, l.Text)
	}
	b.WriteString("\n候选强化：" + strings.Join(candidates, "、") + "\n\n输出 json。")
	var r ScreenRead
	if err := c.ChatJSON(ctx, []Message{{Role: "user", Content: b.String()}}, ChatOptions{MaxTokens: 300}, &r); err != nil {
		return nil, err
	}
	// Keep only names that are candidates.
	ok := map[string]bool{}
	for _, n := range candidates {
		ok[n] = true
	}
	for i, n := range r.Names {
		if !ok[strings.TrimSpace(n)] {
			r.Names[i] = ""
		}
	}
	return &r, nil
}
