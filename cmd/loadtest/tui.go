/*
TelePortal: High-performance, zero-allocation bi-directional audio bridge.
Copyright (C) 2026 Mark Horila

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tuiModel struct {
	// --- Pointer-containing fields (GC scan prefix) ---

	// 24 bytes
	startTime time.Time

	// 8 bytes
	orchestrator *Orchestrator

	// --- Scalar / Non-pointer fields ---

	// 8 bytes
	duration   time.Duration
	total      int
	connected  int
	ws         int
	errs       int
	dtmfSent   int
	dtmfEchoed int
}

type tickMsg time.Time
type stateUpdateMsg struct{}

func doTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func waitForStateUpdate(updates <-chan *Simulator) tea.Cmd {
	return func() tea.Msg {
		<-updates
		return stateUpdateMsg{}
	}
}

func initialModel(orch *Orchestrator) tuiModel {
	return tuiModel{
		orchestrator: orch,
		startTime:    time.Now(),
		duration:     orch.cfg.Duration,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		doTick(),
		waitForStateUpdate(m.orchestrator.StateUpdates()),
	)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
	case tickMsg:
		if time.Since(m.startTime) >= m.duration {
			return m, tea.Quit
		}
		m.total, m.connected, m.ws, m.errs, m.dtmfSent, m.dtmfEchoed = m.orchestrator.GetStats()
		return m, doTick()
	case stateUpdateMsg:
		m.total, m.connected, m.ws, m.errs, m.dtmfSent, m.dtmfEchoed = m.orchestrator.GetStats()
		return m, waitForStateUpdate(m.orchestrator.StateUpdates())
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).MarginBottom(1)
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Width(20)
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).BorderForeground(lipgloss.Color("62"))
)

func (m tuiModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("TelePortal Load Test Dashboard"))
	b.WriteString("\n")

	elapsed := time.Since(m.startTime).Round(time.Second)
	remaining := m.duration - elapsed
	if remaining < 0 {
		remaining = 0
	}

	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("Time Elapsed:"), valueStyle.Render(elapsed.String())))
	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("Time Remaining:"), valueStyle.Render(remaining.String())))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("Total Calls:"), valueStyle.Render(fmt.Sprint(m.total))))
	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("SIP Connected:"), valueStyle.Render(fmt.Sprint(m.connected))))
	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("WS Established:"), valueStyle.Render(fmt.Sprint(m.ws))))

	if m.orchestrator.cfg.DTMF > 0 {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("DTMF Sent:"), valueStyle.Render(fmt.Sprint(m.dtmfSent))))

		echoStr := fmt.Sprint(m.dtmfEchoed)
		if m.dtmfEchoed < m.dtmfSent {
			echoStr = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render(echoStr) // Orange/Yellowish
		} else {
			echoStr = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Render(echoStr) // Green
		}
		b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("DTMF Echoed:"), echoStr))
	}

	errStr := fmt.Sprint(m.errs)
	if m.errs > 0 {
		errStr = errorStyle.Render(errStr)
	} else {
		errStr = valueStyle.Render(errStr)
	}
	b.WriteString(fmt.Sprintf("%s%s\n", labelStyle.Render("Errors:"), errStr))

	b.WriteString("\nPress 'q' or 'ctrl+c' to stop gracefully.")

	return boxStyle.Render(b.String())
}
