// SPDX-License-Identifier: MIT

package router

import (
	"regexp"
	"slices"
	"strings"

	"github.com/wooinwoo/freeway-cli/backend/internal/contracts"
)

// Picker priority for benchmarked models, based on the Artificial Analysis Intelligence
// Index and published model comparisons. Keep exact revisions and leave unscored models below.
// Scores from Intelligence Index v5.0.2, checked 2026-11-26, follow each later entry.
// https://artificialanalysis.ai/leaderboards/models/
// https://artificialanalysis.ai/models/comparisons/minimax-m3-1-flash-vs-deepseek-v4-1-flash
var modelQualityOrder = []string{
	"model/deepseek/deepseek-v4.1-flash-1017",
	"model/minimax/minimax-m3.1-flash",
	"model/deepseek/deepseek-v4.1-flash",
	"model/zai/glm-5.3-flash",
	"opencode/mimo-v2.6-flash-free", // 26.8
	"model/deepseek/deepseek-v4-pro-0813",
	"model/reflection/beam-1.0",
	"model/nvidia/nemotron-3.5-super-vl",
	"model/nvidia/nemotron-3-ultra-550b-a55b",
	"model/nvidia/nemotron-3-ultra",
	"model/nvidia/muse-glimmer-30b", // 6.3
	"model/liquid/lfm-3-1.7b",       // 6.0
	"model/nvidia/nemotron-3.5-lightning",
	// Requesty's full name for the same model, 5.5.
	"model/nvidia/nemotron-3.5-lightning-30b-a3b",
	"model/google/gemma-4-31b-it", // 4.4, reasoning
	"model/openai/gpt-oss-120b",
}

func modelQualityRank(id string) int {
	if id == "model/reflection/beam" {
		id = "model/reflection/beam-1.0"
	}
	if index := slices.Index(modelQualityOrder, id); index >= 0 {
		return index
	}
	return len(modelQualityOrder)
}

// Terminal-Bench 4.0 results from Artificial Analysis, checked 2026-11-20.
// Prefer exact revisions only. The published reasoning profile can differ from
// a free provider's runtime default, so this is evidence about the model family.
// https://artificialanalysis.ai/evaluations/terminalbench-4-0
// https://artificialanalysis.ai/models/comparisons/deepseek-v4-1-flash-vs-deepseek-v4-1-flash-1017
// https://artificialanalysis.ai/models/comparisons/minimax-m3-1-flash-vs-deepseek-v4-1-flash
// https://artificialanalysis.ai/models/comparisons/deepseek-v4-1-flash-vs-deepseek-v4-pro
// Later entries use Terminal-Bench 4.1 / FrontierCode 2.0 from the Intelligence Index v5.0.2
// leaderboard, checked 2026-11-27. Free models scoring near zero on both are left unranked.
// https://artificialanalysis.ai/leaderboards/models
var codingQualityOrder = []string{
	"model/deepseek/deepseek-v4.1-flash-1017",
	"model/deepseek/deepseek-v4.1-flash",
	"model/minimax/minimax-m3.1-flash",
	"opencode/mimo-v2.6-flash-free", // 23.7 / 21.6
	"model/deepseek/deepseek-v4-pro-0813",
	"model/reflection/beam",              // 3.0 / 5.1; the catalog groups Beam 1.0 under this ID
	"model/nvidia/nemotron-3.5-super-vl", // 0.0 / 6.1
}

// HLE-Diamond scores in the same Artificial Analysis comparisons above, then the
// v5.0.2 leaderboard for the free models added on 2026-11-27.
var reasoningQualityOrder = []string{
	"model/minimax/minimax-m3.1-flash",
	"model/deepseek/deepseek-v4.1-flash-1017",
	"model/deepseek/deepseek-v4-pro-0813",
	"model/deepseek/deepseek-v4.1-flash",
	"opencode/mimo-v2.6-flash-free",           // 13.7
	"model/reflection/beam",                   // 12.8
	"model/nvidia/muse-glimmer-30b",           // 9.1
	"model/nvidia/nemotron-3.5-super-vl",      // 8.8
	"model/nvidia/nemotron-3-ultra-550b-a55b", // 8.5
	"model/nvidia/nemotron-3-ultra",
	"model/google/gemma-4-31b-it", // 7.2
}

// MMMU-Pro on the Artificial Analysis v5.0.2 leaderboard, checked 2026-11-27, for the free
// routes that take images. MiMo-V2.6-Flash scores 73.1 but is offered only through OpenCode,
// which receives prompts as text.
var visionQualityOrder = []string{
	"model/nvidia/muse-glimmer-30b", // 74.3
	"model/google/gemma-4-31b-it",   // 73.4
}

func visionRank(id string) int {
	if index := slices.Index(visionQualityOrder, id); index >= 0 {
		return index
	}
	return len(visionQualityOrder)
}

const codingFilename = `[\p{L}\w/-]+\.(?:go|py|rs|tsx|ts|jsx|js|kt|swift|java|cpp|cs|rb|sh|sql)\b`

var (
	codingIntent = regexp.MustCompile(`(?i)\b(?:implement|refactor|debug|fix|write|review|explain|test)\b[^\n.!?]{0,110}` +
		`(?:\b(?:code|function|class|method|script|tests?|compiler|typescript|javascript|python|golang|rust|sql|stack trace|pull request)\b|` + codingFilename + `)|` +
		`(?:코드|함수|스크립트|컴파일|타입스크립트|파이썬|단위 테스트|` + codingFilename + `)[^\n.!?]{0,30}(?:구현|작성|만들어|수정|설명|검토|리팩터링|디버깅|고쳐)`)
	reasoningIntent = regexp.MustCompile(`(?i)\b(prove (that|the|this|whether)|derive (the|this|an?))\b|(?:정리|명제|공식|수식|방정식)[^\n.!?]{0,25}(증명|유도)`)
	proseIntent     = regexp.MustCompile(`(?i)^(?:(?:please\s+)?(?:translate|proofread|summarize)\b|(?:(?:다음|아래|이)\s*(?:문장|문구|글|내용)[^:\n.!?]{0,35})?(?:번역|요약|교정))`)
)

func taskBenchmark(messages []contracts.InputMessage) []string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			// The CLI appends selected files as reference text after the prompt.
			prompt, _, _ := strings.Cut(messages[i].Content, "\n\nAttached file (reference data):\n")
			switch {
			case proseIntent.MatchString(strings.TrimSpace(prompt)):
				return nil
			case codingIntent.MatchString(prompt):
				return codingQualityOrder
			case reasoningIntent.MatchString(prompt):
				return reasoningQualityOrder
			}
			break
		}
	}
	return nil
}

func (r *Router) prioritizeBenchmark(catalog []route, order []string) {
	var indexes []int
	var comparable []route
	for i, candidate := range catalog {
		if _, preferred := r.preference(candidate); !preferred && slices.Contains(order, candidate.groupID) {
			indexes = append(indexes, i)
			comparable = append(comparable, candidate)
		}
	}
	// Leave unmeasured models in their general positions. Benchmark coverage
	// alone is insufficient evidence for moving a model up or down.
	slices.SortStableFunc(comparable, func(a, b route) int { return slices.Index(order, a.groupID) - slices.Index(order, b.groupID) })
	for i, index := range indexes {
		catalog[index] = comparable[i]
	}
}
