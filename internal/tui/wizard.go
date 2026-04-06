package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

// wizardStep tracks the current step in the KB creation wizard.
type wizardStep int

const (
	wizStepID wizardStep = iota
	wizStepName
	wizStepEmbedProvider
	wizStepEmbedModel
	wizStepEmbedAPIKey
	wizStepLLMProvider
	wizStepLLMModel
	wizStepLLMAPIKey
	wizStepConfirm
	wizStepCount // sentinel
)

func (s wizardStep) label() string {
	switch s {
	case wizStepID:
		return "Knowledge Base ID"
	case wizStepName:
		return "Display Name (optional)"
	case wizStepEmbedProvider:
		return "Embedding Provider"
	case wizStepEmbedModel:
		return "Embedding Model"
	case wizStepEmbedAPIKey:
		return "Embedding API Key (optional, uses env)"
	case wizStepLLMProvider:
		return "LLM Provider"
	case wizStepLLMModel:
		return "LLM Model"
	case wizStepLLMAPIKey:
		return "LLM API Key (optional, uses env)"
	case wizStepConfirm:
		return "Confirm"
	default:
		return ""
	}
}

func (s wizardStep) hint() string {
	switch s {
	case wizStepID:
		return "unique identifier, e.g. my-project"
	case wizStepName:
		return "press enter to use ID as name"
	case wizStepEmbedProvider:
		return "ollama, openai, gemini, vertex, azure, groq"
	case wizStepEmbedModel:
		return "e.g. gemini-embedding-001, nomic-embed-text"
	case wizStepEmbedAPIKey:
		return "leave empty to use GEMINI_API_KEY / OPENAI_API_KEY env"
	case wizStepLLMProvider:
		return "ollama, openai, gemini, vertex, azure, groq"
	case wizStepLLMModel:
		return "e.g. gemini-2.5-flash, llama3.2, gpt-4o-mini"
	case wizStepLLMAPIKey:
		return "leave empty to use same as embed or env var"
	case wizStepConfirm:
		return "press enter to create, esc to cancel"
	default:
		return ""
	}
}

// kbWizard is a multi-step KB creation form.
type kbWizard struct {
	step   wizardStep
	inputs [wizStepCount]textinput.Model
	store  storage.Store
	err    string
}

func newKBWizard(store storage.Store) kbWizard {
	var inputs [wizStepCount]textinput.Model
	for i := wizStepID; i < wizStepCount; i++ {
		ti := textinput.New()
		ti.CharLimit = 200
		ti.Width = 50
		ti.Placeholder = i.hint()
		inputs[i] = ti
	}

	// Set defaults.
	inputs[wizStepEmbedProvider].SetValue("gemini")
	inputs[wizStepEmbedModel].SetValue("gemini-embedding-001")
	inputs[wizStepLLMProvider].SetValue("gemini")
	inputs[wizStepLLMModel].SetValue("gemini-2.5-flash")

	inputs[wizStepID].Focus()

	return kbWizard{
		step:   wizStepID,
		inputs: inputs,
		store:  store,
	}
}

func (w *kbWizard) focused() *textinput.Model {
	if w.step >= wizStepCount {
		return nil
	}
	return &w.inputs[w.step]
}

func (w *kbWizard) next() {
	if w.step < wizStepConfirm {
		w.inputs[w.step].Blur()
		w.step++
		if w.step < wizStepCount {
			w.inputs[w.step].Focus()
		}
	}
}

func (w *kbWizard) prev() {
	if w.step > wizStepID {
		w.inputs[w.step].Blur()
		w.step--
		w.inputs[w.step].Focus()
	}
}

func (w *kbWizard) val(step wizardStep) string {
	return strings.TrimSpace(w.inputs[step].Value())
}

func (w *kbWizard) validate() string {
	if w.val(wizStepID) == "" {
		return "ID is required"
	}
	if w.val(wizStepEmbedProvider) == "" {
		return "Embedding provider is required"
	}
	if w.val(wizStepEmbedModel) == "" {
		return "Embedding model is required"
	}
	if w.val(wizStepLLMProvider) == "" {
		return "LLM provider is required"
	}
	if w.val(wizStepLLMModel) == "" {
		return "LLM model is required"
	}
	return ""
}

type kbCreatedMsg struct {
	kb  *domain.KnowledgeBase
	err error
}

func (w *kbWizard) createKB() tea.Cmd {
	id := w.val(wizStepID)
	name := w.val(wizStepName)
	if name == "" {
		name = id
	}
	embedProvider := w.val(wizStepEmbedProvider)
	embedModel := w.val(wizStepEmbedModel)
	embedKey := w.val(wizStepEmbedAPIKey)
	llmProvider := w.val(wizStepLLMProvider)
	llmModel := w.val(wizStepLLMModel)
	llmKey := w.val(wizStepLLMAPIKey)
	if llmKey == "" {
		llmKey = embedKey
	}

	store := w.store
	return func() tea.Msg {
		kb := &domain.KnowledgeBase{
			ID:   id,
			Name: name,
			EmbedConfig: domain.EmbedConfig{
				Provider: embedProvider,
				Model:    embedModel,
				APIKey:   embedKey,
			},
			LLMConfig: domain.LLMConfig{
				Provider: llmProvider,
				Model:    llmModel,
				APIKey:   llmKey,
			},
			CreatedAt: time.Now().UTC(),
		}
		err := store.CreateKB(context.Background(), kb)
		return kbCreatedMsg{kb: kb, err: err}
	}
}

func (w *kbWizard) update(msg tea.KeyMsg) (bool, tea.Cmd) {
	key := msg.String()
	w.err = ""

	switch key {
	case "esc":
		return true, nil // cancelled
	case "enter":
		if w.step == wizStepConfirm {
			if errMsg := w.validate(); errMsg != "" {
				w.err = errMsg
				return false, nil
			}
			return false, w.createKB()
		}
		w.next()
		return false, textinput.Blink
	case "shift+tab":
		w.prev()
		return false, textinput.Blink
	case "tab":
		w.next()
		return false, textinput.Blink
	}

	// Pass to active input.
	if w.step < wizStepCount {
		var cmd tea.Cmd
		w.inputs[w.step], cmd = w.inputs[w.step].Update(msg)
		return false, cmd
	}
	return false, nil
}

func (w *kbWizard) view(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Create Knowledge Base") + "\n\n")

	for i := wizStepID; i < wizStepCount; i++ {
		lbl := i.label()
		if i == w.step {
			lbl = selectedStyle.Render("▸ " + lbl)
		} else {
			lbl = mutedStyle.Render("  " + lbl)
		}
		b.WriteString(lbl + "\n")

		if i == wizStepConfirm {
			// Show summary at confirm step.
			b.WriteString(w.renderSummary())
		} else if i <= w.step {
			b.WriteString("    " + w.inputs[i].View() + "\n")
		} else {
			val := w.inputs[i].Value()
			if val != "" {
				b.WriteString("    " + mutedStyle.Render(val) + "\n")
			} else {
				b.WriteString("    " + mutedStyle.Render("—") + "\n")
			}
		}
		b.WriteString("\n")
	}

	if w.err != "" {
		b.WriteString(errorStyle.Render("Error: "+w.err) + "\n")
	}

	b.WriteString(helpStyle.Render("tab/enter: next  shift+tab: back  esc: cancel"))

	w2 := min(width-4, 65)
	h := min(height-2, 30)
	return activePaneStyle.Width(w2).Height(h).Padding(1, 2).Render(b.String())
}

func (w *kbWizard) renderSummary() string {
	id := w.val(wizStepID)
	name := w.val(wizStepName)
	if name == "" {
		name = id
	}
	apiKeyDisplay := func(key string) string {
		if key == "" {
			return mutedStyle.Render("(from env)")
		}
		if len(key) > 8 {
			return key[:4] + "..." + key[len(key)-4:]
		}
		return "****"
	}

	return fmt.Sprintf(
		"    %s %s\n    %s %s\n    %s %s/%s  key: %s\n    %s %s/%s  key: %s\n",
		labelStyle.Render("ID:"), id,
		labelStyle.Render("Name:"), name,
		labelStyle.Render("Embed:"), w.val(wizStepEmbedProvider), w.val(wizStepEmbedModel), apiKeyDisplay(w.val(wizStepEmbedAPIKey)),
		labelStyle.Render("LLM:"), w.val(wizStepLLMProvider), w.val(wizStepLLMModel), apiKeyDisplay(w.val(wizStepLLMAPIKey)),
	)
}

// confirmDialog is a simple yes/no confirmation.
type confirmDialog struct {
	title   string
	message string
	onYes   tea.Cmd
}

func (c *confirmDialog) view(width, height int) string {
	content := titleStyle.Render(c.title) + "\n\n" +
		c.message + "\n\n" +
		labelStyle.Render("y") + " confirm  " +
		labelStyle.Render("n/esc") + " cancel"

	w := min(width-4, 50)
	h := min(height-2, 10)
	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(colorDanger).
		Width(w).Height(h).
		Padding(1, 2).
		Render(content)
}
