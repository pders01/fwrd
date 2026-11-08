package tui

import tea "github.com/charmbracelet/bubbletea"

// A media chooser overlays the reader; its same-target renders remain useful.
func (a *App) readerVisible() bool {
	return a.view == ViewReader || (a.view == ViewMedia && a.mediaPreviousView == ViewReader)
}

// Reader navigation/completions must not stop a refresh-owned spinner.
func (a *App) releaseReaderLoading() {
	a.loadingArticle = false
	if a.spinnerLabel == MsgLoadingArticle {
		a.stopSpinner()
	}
}

func (a *App) resumeReader() tea.Cmd {
	if !a.readerVisible() || a.currentArticle == nil {
		return nil
	}
	a.loadingArticle = a.readerInitialPending
	render := a.renderArticle(a.currentArticle)
	if a.loadingArticle {
		// Do not replace another operation's active spinner.
		if !a.spinnerActive || a.spinnerLabel == MsgLoadingArticle {
			return tea.Batch(a.startSpinner(MsgLoadingArticle), render)
		}
	}
	return render
}
