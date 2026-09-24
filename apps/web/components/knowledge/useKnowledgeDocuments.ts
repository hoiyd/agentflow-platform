"use client";

import { useEffect, useState, type ChangeEvent } from "react";
import { createDocument, deleteDocument, getDocument, listDocuments, uploadDocument, type DocumentDetail, type DocumentInfo } from "../../lib/knowledge-api";
import { createLatestRequestController } from "../../lib/latest-request";

export function useKnowledgeDocuments(onDeleteDocument: (documentId: string) => void) {
  const [documents, setDocuments] = useState<DocumentInfo[]>([]);
  const [documentTitle, setDocumentTitle] = useState("");
  const [documentContent, setDocumentContent] = useState("");
  const [isCreating, setIsCreating] = useState(false);
  const [uploadTitle, setUploadTitle] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [selectedDocument, setSelectedDocument] = useState<DocumentDetail | null>(null);
  const [selectedDocumentId, setSelectedDocumentId] = useState("");
  const [isLoadingDocumentDetail, setIsLoadingDocumentDetail] = useState(false);
  const [deletingDocumentId, setDeletingDocumentId] = useState("");
  const [error, setError] = useState("");
  const [listRequests] = useState(createLatestRequestController);
  const [detailRequests] = useState(createLatestRequestController);

  useEffect(() => () => { listRequests.cancel(); detailRequests.cancel(); }, [listRequests, detailRequests]);

  async function refreshDocuments() {
    const request = listRequests.begin();
    setError("");
    try {
      const items = await listDocuments(request.signal);
      if (!request.isCurrent()) return;
      detailRequests.cancel();
      setIsLoadingDocumentDetail(false);
      setDocuments(items);
      setSelectedDocument(null);
      setSelectedDocumentId("");
    } catch (refreshError) {
      if (request.isCurrent()) setError(refreshError instanceof Error ? refreshError.message : "Failed to load documents");
    }
  }

  async function createTextDocument() {
    const title = documentTitle.trim();
    const content = documentContent.trim();
    if (!title || !content || isCreating) return;
    setIsCreating(true);
    setError("");
    try {
      const created = await createDocument({ title, content, metadata: { source: "ui" } });
      listRequests.cancel();
      setDocuments((items) => [created, ...items.filter((item) => item.id !== created.id)]);
      setDocumentTitle("");
      setDocumentContent("");
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Failed to create document");
    } finally {
      setIsCreating(false);
    }
  }

  function selectUploadFile(event: ChangeEvent<HTMLInputElement>) {
    setUploadFile(event.target.files?.[0] ?? null);
  }

  async function uploadKnowledgeDocument() {
    if (!uploadFile || isUploading) return;
    setIsUploading(true);
    setError("");
    try {
      const created = await uploadDocument({ file: uploadFile, title: uploadTitle });
      listRequests.cancel();
      setDocuments((items) => [created, ...items.filter((item) => item.id !== created.id)]);
      setUploadFile(null);
      setUploadTitle("");
    } catch (uploadError) {
      setError(uploadError instanceof Error ? uploadError.message : "Failed to upload document");
    } finally {
      setIsUploading(false);
    }
  }

  async function selectDocument(documentId: string) {
    listRequests.cancel();
    const request = detailRequests.begin();
    setSelectedDocumentId(documentId);
    setSelectedDocument(null);
    setIsLoadingDocumentDetail(true);
    setError("");
    try {
      const detail = await getDocument(documentId, request.signal);
      if (request.isCurrent()) setSelectedDocument(detail);
    } catch (selectionError) {
      if (request.isCurrent()) setError(selectionError instanceof Error ? selectionError.message : "Failed to load document");
    } finally {
      if (request.isCurrent()) setIsLoadingDocumentDetail(false);
    }
  }

  async function removeDocument(document: DocumentInfo) {
    if (deletingDocumentId || !window.confirm(`Delete knowledge document "${document.title}"? This cannot be undone.`)) return;
    setDeletingDocumentId(document.id);
    setError("");
    try {
      await deleteDocument(document.id);
      listRequests.cancel();
      setDocuments((items) => items.filter((item) => item.id !== document.id));
      onDeleteDocument(document.id);
      if (selectedDocumentId === document.id) {
        detailRequests.cancel();
        setIsLoadingDocumentDetail(false);
        setSelectedDocumentId("");
        setSelectedDocument(null);
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "Failed to delete document");
    } finally {
      setDeletingDocumentId("");
    }
  }

  return {
    documents, documentTitle, documentContent, isCreating, uploadTitle, uploadFile, isUploading,
    selectedDocument, selectedDocumentId, isLoadingDocumentDetail, deletingDocumentId, error,
    refreshDocuments, createTextDocument, selectUploadFile, uploadKnowledgeDocument, selectDocument, removeDocument,
    setDocumentTitle, setDocumentContent, setUploadTitle
  };
}

export type KnowledgeDocumentsModel = ReturnType<typeof useKnowledgeDocuments>;
