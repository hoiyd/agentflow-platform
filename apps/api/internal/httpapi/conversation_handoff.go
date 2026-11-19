package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
)

const maxHandoffCharacters = 4000

func (h *Handler) getConversationHandoff(w http.ResponseWriter, r *http.Request) {
	scoped := h.scopedStore(r)
	conversationID := r.PathValue("id")
	state, _, err := scoped.GetTaskState(conversationID)
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	summary, found, err := scoped.GetLatestContextCompaction(conversationID)
	if err != nil {
		writeTaskStateError(w, r, err)
		return
	}
	sources := "- Conversation: /workspace?conversation=" + url.QueryEscape(conversationID)
	if state.Version > 0 {
		sources += fmt.Sprintf("\n- Task State revision: %d", state.Version)
	}
	if found {
		run, accessible, err := scoped.GetRun(summary.RunID)
		if err != nil {
			writeTaskStateError(w, r, err)
			return
		}
		// Use only a scoped reference: historical summary prose can contain superseded instructions.
		if accessible && run.ConversationID == conversationID {
			sources += fmt.Sprintf("\n- Context summary generation %d: /runs/%s", summary.Generation, url.PathEscape(run.ID))
		}
	}
	result := buildConversationHandoff(state, sources)
	if state.Version == 0 && !found {
		result.Draft = ""
	}
	writeJSON(w, http.StatusOK, result)
}

func buildConversationHandoff(state domain.TaskState, sources string) apicontract.ConversationHandoff {
	sections := []string{}
	if goal := strings.Join(strings.Fields(state.Goal), " "); goal != "" {
		sections = append(sections, "Goal\n"+goal)
	}
	addSection := func(title string, items []string) {
		if len(items) > 0 {
			sections = append(sections, title+"\n- "+strings.Join(items, "\n- "))
		}
	}
	var constraints, tasks, blockers []string
	for _, constraint := range state.Constraints {
		constraints = append(constraints, strings.Join(strings.Fields(constraint.Statement), " "))
	}
	for _, task := range state.Tasks {
		if task.Status == domain.TaskItemPending || task.Status == domain.TaskItemInProgress {
			text := task.Title
			if task.Details != "" {
				text += ": " + task.Details
			}
			tasks = append(tasks, strings.Join(strings.Fields(text), " "))
		}
	}
	for _, blocker := range state.Blockers {
		if blocker.Status == domain.TaskBlockerOpen {
			blockers = append(blockers, strings.Join(strings.Fields(blocker.Description), " "))
		}
	}
	addSection("Constraints", constraints)
	addSection("Unfinished work", tasks)
	addSection("Open blockers", blockers)
	evidence := "Sources\n" + sources
	if len(sections) == 0 {
		return apicontract.ConversationHandoff{Draft: evidence, MaxCharacters: maxHandoffCharacters}
	}
	body := []rune(strings.Join(sections, "\n\n"))
	limit := maxHandoffCharacters - utf8.RuneCountInString(evidence) - 2
	truncated := len(body) > limit
	if truncated {
		body = append(body[:limit-3], '.', '.', '.')
	}
	return apicontract.ConversationHandoff{
		Draft: string(body) + "\n\n" + evidence, MaxCharacters: maxHandoffCharacters, Truncated: truncated,
	}
}
