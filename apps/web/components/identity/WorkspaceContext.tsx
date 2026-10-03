"use client";
import { createContext, useContext } from "react";

export const WorkspaceReadOnly = createContext(false);
export function useWorkspaceReadOnly(): boolean { return useContext(WorkspaceReadOnly); }

// Agent profiles and Tool enablement remain service-wide operator configuration.
export const ServiceConfigurationReadOnly = createContext(false);
export function useServiceConfigurationReadOnly(): boolean { return useContext(ServiceConfigurationReadOnly); }

// Mirrors production command-verifier availability; backend still enforces it.
// A missing session must never opt a consumer into host execution.
export const TrustedHostCommands = createContext(false);
