package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestAgentAndRunHandlersProjectStoreFailures(t *testing.T) {
	httpStore, workspace, fileStore := newBoundaryHTTPStore(t)
	handler := &Handler{store: httpStore}
	want := errors.New("persistence unavailable")

	httpStore.listAgentsErr = want
	assertHandlerFailure(t, handler.listAgents, httptest.NewRequest(http.MethodGet, "/api/agents", nil), http.StatusInternalServerError)
	httpStore.listAgentsErr = nil

	httpStore.createAgentErr = want
	assertHandlerFailure(t, handler.createAgent, httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewBufferString(`{"name":"Coverage Agent"}`)), http.StatusBadRequest)
	httpStore.createAgentErr = nil

	httpStore.getAgentErr = want
	assertHandlerFailure(t, handler.getAgent, httptest.NewRequest(http.MethodGet, "/api/agents/agent_planner", nil), http.StatusInternalServerError)
	httpStore.getAgentErr = nil

	httpStore.archiveAgentErr = want
	assertHandlerFailure(t, handler.archiveAgent, httptest.NewRequest(http.MethodDelete, "/api/agents/custom", nil), http.StatusBadRequest)
	httpStore.archiveAgentErr = nil

	httpStore.getAgentErr = want
	assertHandlerFailure(t, handler.updateAgent, httptest.NewRequest(http.MethodPatch, "/api/agents/agent_planner", bytes.NewBufferString(`{}`)), http.StatusInternalServerError)
	httpStore.getAgentErr = nil
	assertHandlerFailure(t, handler.updateAgent, httptest.NewRequest(http.MethodPatch, "/api/agents/agent_planner", bytes.NewBufferString(`{"tools":["not-installed"]}`)), http.StatusBadRequest)
	httpStore.updateAgentErr = want
	assertHandlerFailure(t, handler.updateAgent, httptest.NewRequest(http.MethodPatch, "/api/agents/agent_planner", bytes.NewBufferString(`{}`)), http.StatusBadRequest)
	httpStore.updateAgentErr = nil

	conversation, err := fileStore.CreateConversation("run failures")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	workspace.listRunsErr = want
	assertHandlerFailure(t, handler.listRuns, httptest.NewRequest(http.MethodGet, "/api/runs", nil), http.StatusInternalServerError)
	workspace.listRunsErr = nil
	workspace.getRunErr = want
	for _, test := range []struct {
		invoke  func(http.ResponseWriter, *http.Request)
		request *http.Request
	}{
		{invoke: handler.getRun, request: httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID, nil)},
		{invoke: handler.cancelRun, request: httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil)},
		{invoke: handler.listCollaborationSteps, request: httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/collaboration_steps", nil)},
		{invoke: handler.getRunReplay, request: httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil)},
		{invoke: handler.getRunUsage, request: httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/usage", nil)},
	} {
		assertHandlerFailure(t, test.invoke, test.request, http.StatusInternalServerError)
	}
	workspace.getRunErr = nil

	workspace.listCollaborationStepsErr = want
	assertHandlerFailure(t, handler.listCollaborationSteps, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/collaboration_steps", nil), http.StatusInternalServerError)
	workspace.listCollaborationStepsErr = nil
	workspace.getRunReplayErr = want
	assertHandlerFailure(t, handler.getRunReplay, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil), http.StatusInternalServerError)
	workspace.getRunReplayErr = nil
	workspace.getRunUsageErr = want
	assertHandlerFailure(t, handler.getRunUsage, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/usage", nil), http.StatusInternalServerError)
}

func TestConversationAndDocumentHandlersProjectWorkspaceFailures(t *testing.T) {
	httpStore, workspace, fileStore := newBoundaryHTTPStore(t)
	handler := &Handler{store: httpStore}
	want := errors.New("workspace store failed")

	workspace.listConversationsErr = want
	assertHandlerFailure(t, handler.listConversations, httptest.NewRequest(http.MethodGet, "/api/conversations", nil), http.StatusInternalServerError)
	workspace.listConversationsErr = nil
	workspace.createConversationErr = want
	assertHandlerFailure(t, handler.createConversation, httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewBufferString(`{"title":"coverage"}`)), http.StatusInternalServerError)
	workspace.createConversationErr = nil

	conversation, err := fileStore.CreateConversation("coverage")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	workspace.updateConversationTitleErr = want
	assertHandlerFailure(t, handler.updateConversation, httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conversation.ID, bytes.NewBufferString(`{"title":"updated"}`)), http.StatusInternalServerError)
	workspace.updateConversationTitleErr = nil
	workspace.getConversationErr = want
	assertHandlerFailure(t, handler.updateConversation, httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conversation.ID, bytes.NewBufferString(`{"title":"updated"}`)), http.StatusInternalServerError)
	assertHandlerFailure(t, handler.listMessages, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversation.ID+"/messages", nil), http.StatusInternalServerError)
	workspace.getConversationErr = nil
	workspace.deleteConversationErr = want
	assertHandlerFailure(t, handler.deleteConversation, httptest.NewRequest(http.MethodDelete, "/api/conversations/"+conversation.ID, nil), http.StatusInternalServerError)
	workspace.deleteConversationErr = nil
	workspace.listMessagesErr = want
	assertHandlerFailure(t, handler.listMessages, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversation.ID+"/messages", nil), http.StatusInternalServerError)

	workspace.listDocumentsErr = want
	assertHandlerFailure(t, handler.listDocuments, httptest.NewRequest(http.MethodGet, "/api/documents", nil), http.StatusInternalServerError)
	workspace.listDocumentsErr = nil
	workspace.getDocumentErr = want
	assertHandlerFailure(t, handler.getDocument, httptest.NewRequest(http.MethodGet, "/api/documents/document-1", nil), http.StatusInternalServerError)
}

func TestKnowledgeAndEpisodeHandlersProjectDependencyFailures(t *testing.T) {
	want := errors.New("dependency failed")
	knowledge := &boundaryKnowledgeOperations{err: want}
	handler := &Handler{knowledge: knowledge}
	assertHandlerFailure(t, handler.createDocument, httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"title":"Runbook","content":"details"}`)), http.StatusBadRequest)
	body, contentType := multipartUploadBody(t, "runbook.md", "# Runbook")
	upload := httptest.NewRequest(http.MethodPost, "/api/documents/upload", body)
	upload.Header.Set("Content-Type", contentType)
	assertHandlerFailure(t, handler.uploadDocument, upload, http.StatusBadRequest)

	httpStore, workspace, fileStore := newBoundaryHTTPStore(t)
	conversation, err := fileStore.CreateConversation("episode")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	episodeHandler := &Handler{store: httpStore}
	request := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/episode", nil) }
	workspace.getRunErr = want
	assertHandlerFailure(t, episodeHandler.getEpisodeReport, request(), http.StatusInternalServerError)
	workspace.getRunErr = nil
	workspace.getRunReplayErr = want
	assertHandlerFailure(t, episodeHandler.getEpisodeReport, request(), http.StatusInternalServerError)
	workspace.getRunReplayErr = nil
	httpStore.getAgentErr = want
	assertHandlerFailure(t, episodeHandler.getEpisodeReport, request(), http.StatusInternalServerError)
}

func assertHandlerFailure(t *testing.T, invoke func(http.ResponseWriter, *http.Request), request *http.Request, status int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	invoke(recorder, request)
	if recorder.Code != status {
		t.Fatalf("expected status %d, got %d body=%s", status, recorder.Code, recorder.Body.String())
	}
	response := decodeAPIErrorResponse(t, recorder)
	if response.Code == "" || response.RequestID == "" {
		t.Fatalf("expected structured failure response, got %#v", response)
	}
}

func newBoundaryHTTPStore(t *testing.T) (*boundaryHTTPStore, *boundaryWorkspaceStore, *store.FileStore) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	workspace := &boundaryWorkspaceStore{
		WorkspaceStore: fileStore.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID)),
	}
	return &boundaryHTTPStore{Store: fileStore, workspace: workspace}, workspace, fileStore
}

type boundaryHTTPStore struct {
	store.Store
	workspace       store.WorkspaceStore
	listAgentsErr   error
	createAgentErr  error
	getAgentErr     error
	updateAgentErr  error
	archiveAgentErr error
}

func (s *boundaryHTTPStore) ForWorkspace(domain.WorkspaceScope) store.WorkspaceStore {
	return s.workspace
}
func (s *boundaryHTTPStore) ListAgents() ([]domain.Agent, error) {
	if s.listAgentsErr != nil {
		return nil, s.listAgentsErr
	}
	return s.Store.ListAgents()
}
func (s *boundaryHTTPStore) CreateAgent(agent domain.Agent) (domain.Agent, error) {
	if s.createAgentErr != nil {
		return domain.Agent{}, s.createAgentErr
	}
	return s.Store.CreateAgent(agent)
}
func (s *boundaryHTTPStore) GetAgent(id string) (domain.Agent, bool, error) {
	if s.getAgentErr != nil {
		return domain.Agent{}, false, s.getAgentErr
	}
	return s.Store.GetAgent(id)
}
func (s *boundaryHTTPStore) UpdateAgent(agent domain.Agent) (domain.Agent, error) {
	if s.updateAgentErr != nil {
		return domain.Agent{}, s.updateAgentErr
	}
	return s.Store.UpdateAgent(agent)
}
func (s *boundaryHTTPStore) ArchiveAgent(id string) error {
	if s.archiveAgentErr != nil {
		return s.archiveAgentErr
	}
	return s.Store.ArchiveAgent(id)
}

type boundaryWorkspaceStore struct {
	store.WorkspaceStore
	listConversationsErr       error
	createConversationErr      error
	getConversationErr         error
	deleteConversationErr      error
	updateConversationTitleErr error
	listMessagesErr            error
	getRunErr                  error
	listRunsErr                error
	listCollaborationStepsErr  error
	getRunReplayErr            error
	getRunUsageErr             error
	listDocumentsErr           error
	getDocumentErr             error
}

func (s *boundaryWorkspaceStore) ListConversations() ([]domain.Conversation, error) {
	if s.listConversationsErr != nil {
		return nil, s.listConversationsErr
	}
	return s.WorkspaceStore.ListConversations()
}
func (s *boundaryWorkspaceStore) CreateConversation(title string) (domain.Conversation, error) {
	if s.createConversationErr != nil {
		return domain.Conversation{}, s.createConversationErr
	}
	return s.WorkspaceStore.CreateConversation(title)
}
func (s *boundaryWorkspaceStore) GetConversation(id string) (domain.Conversation, bool, error) {
	if s.getConversationErr != nil {
		return domain.Conversation{}, false, s.getConversationErr
	}
	return s.WorkspaceStore.GetConversation(id)
}
func (s *boundaryWorkspaceStore) DeleteConversation(id string) error {
	if s.deleteConversationErr != nil {
		return s.deleteConversationErr
	}
	return s.WorkspaceStore.DeleteConversation(id)
}
func (s *boundaryWorkspaceStore) UpdateConversationTitle(id string, title string) error {
	if s.updateConversationTitleErr != nil {
		return s.updateConversationTitleErr
	}
	return s.WorkspaceStore.UpdateConversationTitle(id, title)
}
func (s *boundaryWorkspaceStore) ListMessages(id string) ([]domain.Message, error) {
	if s.listMessagesErr != nil {
		return nil, s.listMessagesErr
	}
	return s.WorkspaceStore.ListMessages(id)
}
func (s *boundaryWorkspaceStore) GetRun(id string) (domain.Run, bool, error) {
	if s.getRunErr != nil {
		return domain.Run{}, false, s.getRunErr
	}
	return s.WorkspaceStore.GetRun(id)
}
func (s *boundaryWorkspaceStore) ListRuns() ([]domain.Run, error) {
	if s.listRunsErr != nil {
		return nil, s.listRunsErr
	}
	return s.WorkspaceStore.ListRuns()
}
func (s *boundaryWorkspaceStore) ListCollaborationSteps(id string) ([]domain.CollaborationStep, error) {
	if s.listCollaborationStepsErr != nil {
		return nil, s.listCollaborationStepsErr
	}
	return s.WorkspaceStore.ListCollaborationSteps(id)
}
func (s *boundaryWorkspaceStore) GetRunReplay(id string) (domain.RunReplay, bool, error) {
	if s.getRunReplayErr != nil {
		return domain.RunReplay{}, false, s.getRunReplayErr
	}
	return s.WorkspaceStore.GetRunReplay(id)
}
func (s *boundaryWorkspaceStore) GetRunUsageLedger(id string) (domain.RunUsageLedger, bool, error) {
	if s.getRunUsageErr != nil {
		return domain.RunUsageLedger{}, false, s.getRunUsageErr
	}
	return s.WorkspaceStore.GetRunUsageLedger(id)
}
func (s *boundaryWorkspaceStore) ListDocuments() ([]domain.Document, error) {
	if s.listDocumentsErr != nil {
		return nil, s.listDocumentsErr
	}
	return s.WorkspaceStore.ListDocuments()
}
func (s *boundaryWorkspaceStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	if s.getDocumentErr != nil {
		return domain.Document{}, nil, false, s.getDocumentErr
	}
	return s.WorkspaceStore.GetDocument(id)
}

type boundaryKnowledgeOperations struct{ err error }

func (s *boundaryKnowledgeOperations) Ingest(context.Context, domain.DocumentIngestRequest) (domain.Document, error) {
	return domain.Document{}, s.err
}
func (s *boundaryKnowledgeOperations) Search(context.Context, domain.DocumentSearch, int) (domain.DocumentSearchResponse, error) {
	return domain.DocumentSearchResponse{}, s.err
}
func (s *boundaryKnowledgeOperations) Evaluate(context.Context, domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error) {
	return domain.RAGEvaluationRunResponse{}, s.err
}
