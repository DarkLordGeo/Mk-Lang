// Why: the server must read and write LSP 3.17's JSON shapes.
//
// What: the protocol structures (Position, Range, Location, the params and result
// types) and the symbol and completion kinds.
//
// How: only the fields the server uses; unknown fields are ignored on read.

package lsp

// Position is zero-based, the character in the negotiated encoding.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type contentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []contentChange `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type referenceParams struct {
	textDocumentPositionParams
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type documentParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type codeActionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type cancelParams struct {
	ID any `json:"id"`
}

type initializeParams struct {
	Capabilities struct {
		General struct {
			PositionEncodings []string `json:"positionEncodings"`
		} `json:"general"`
		Window struct {
			WorkDoneProgress bool `json:"workDoneProgress"`
		} `json:"window"`
		Workspace struct {
			DidChangeWatchedFiles struct {
				DynamicRegistration bool `json:"dynamicRegistration"`
			} `json:"didChangeWatchedFiles"`
		} `json:"workspace"`
	} `json:"capabilities"`
	RootURI          string `json:"rootUri"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
}

// Diagnostic severities and related information (LSP 3.17).
const severityError = 1

type lspDiagnostic struct {
	Range              Range                `json:"range"`
	Severity           int                  `json:"severity"`
	Code               string               `json:"code,omitempty"`
	CodeDescription    *codeDescription     `json:"codeDescription,omitempty"`
	Source             string               `json:"source"`
	Message            string               `json:"message"`
	Tags               []int                `json:"tags,omitempty"`
	RelatedInformation []relatedInformation `json:"relatedInformation,omitempty"`
}

// codeDescription links a diagnostic's code to its `mk explain` text.
type codeDescription struct {
	Href string `json:"href"`
}

// Diagnostic tags (LSP 3.17 DiagnosticTag).
const tagUnnecessary = 1

// unnecessaryCodes are the codes whose span is code that can go (the
// catalogue's E0208 unused local or import), shown faded.
var unnecessaryCodes = setOf("E0208")

type relatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Version     *int            `json:"version,omitempty"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type textEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type hover struct {
	Contents markupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Symbol kinds (LSP 3.17 SymbolKind) used for Mk items.
const (
	symModule     = 2
	symNamespace  = 3
	symMethod     = 6
	symField      = 8
	symEnum       = 10
	symInterface  = 11
	symFunction   = 12
	symVariable   = 13
	symConstant   = 14
	symEnumMember = 22
	symStruct     = 23
	symTypeParam  = 26
)

type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

// Completion item kinds (LSP 3.17 CompletionItemKind).
const (
	complMethod  = 2
	complField   = 5
	complModule  = 9
	complValue   = 12
	complKeyword = 14
	complStruct  = 22
)

type completionItem struct {
	Label               string          `json:"label"`
	Kind                int             `json:"kind,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	Documentation       *markupContent  `json:"documentation,omitempty"`
	SortText            string          `json:"sortText,omitempty"`
	InsertText          string          `json:"insertText,omitempty"`
	InsertTextFormat    int             `json:"insertTextFormat,omitempty"`
	AdditionalTextEdits []textEdit      `json:"additionalTextEdits,omitempty"`
	Data                *completionData `json:"data,omitempty"`
}

// completionData is what completionItem/resolve needs: the `mk query
// sig` subject of the item, and the document it was offered in.
type completionData struct {
	Subject string `json:"subject,omitempty"`
	URI     string `json:"uri,omitempty"`
}

// Insert text formats and the snippet kind (LSP 3.17).
const (
	formatSnippet = 2
	complSnippet  = 15
)

type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

type workspaceEdit struct {
	Changes map[string][]textEdit `json:"changes"`
}

type codeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	Diagnostics []lspDiagnostic `json:"diagnostics,omitempty"`
	IsPreferred bool            `json:"isPreferred,omitempty"`
	Edit        *workspaceEdit  `json:"edit,omitempty"`
	Command     *command        `json:"command,omitempty"`
}

type semanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

// tokenLegend is semanticTokens' legend: the classes of the Go-style Mk
// highlighter (comment, string, number, keyword, flow word, type), in that
// order; the flow words are LSP's "modifier" type.
var tokenLegend = []string{"comment", "string", "number", "keyword", "modifier", "type",
	"function", "method", "parameter", "variable", "property", "namespace", "enumMember", "macro"}
