"use client";
import { createContext, useContext } from "react";

export const WorkspaceReadOnly = createContext(false);
export function useWorkspaceReadOnly(): boolean { return useContext(WorkspaceReadOnly); }
