// Application models are intentionally separate from the transport contract.
export interface JobRef {
  deploymentId: string;
  namespaceId: string;
  jobId: string;
}
export interface NamespaceRef {
  deploymentId: string;
  namespaceId: string;
}
export interface Namespace {
  namespaceId: string;
  name: string;
  roles: string[];
  capabilities: string[];
  authorizationVersion?: string;
  authorizationExpiresAt?: string;
}
export interface Source {
  deploymentId: string;
  displayName: string;
  status: string;
  namespaces: Namespace[];
  instanceId?: string;
}
export interface Account {
  id: string;
  displayName: string;
}
export interface Preferences {
  revision: string;
  timezone: string;
  appearance: "system" | "light" | "dark";
  refreshSeconds: number;
}
export interface Bootstrap {
  completeness?: string;
  account: Account;
  sources: Source[];
  preferences: Preferences;
  csrfToken?: string;
  authorizationCheckedAt: string;
  authorizationValidUntil?: string;
  mode?: string;
}
export interface Scope {
  deployments: string[];
  namespace?: NamespaceRef;
}
export interface Contribution {
  deploymentId: string;
  namespaceId?: string;
  status: string;
  observedAt?: string;
  message?: string;
}
export interface Meta {
  requestId?: string;
  completeness: string;
  lastFetchedAt?: string;
  dataAsOf?: string;
  contributions: Contribution[];
  nextCursor?: string;
}
export interface Result<T> {
  data: T;
  meta: Meta;
}
export interface Job extends JobRef {
  name?: string;
  phase: string;
  outcome?: string;
  desiredState?: string;
  observationConfidence?: string;
  revision: string;
  createdAt: string;
  updatedAt?: string;
  startedAt?: string;
  completedAt?: string;
  confidenceUpdatedAt?: string;
  owner?: { id?: string; displayName?: string; verified?: boolean };
  target?: {
    name: string;
    generation?: string;
    generationId?: string;
    backend?: string;
  };
  labels?: Record<string, string>;
  scheduler?: {
    nativeId?: string;
    state?: string;
    reason?: string;
    observedAt?: string;
    cluster?: string;
    backend?: string;
  };
  imported?: boolean;
  disposition?: string;
  lifecycle?: {
    startedRecordedAt?: string;
    startedProvenance?: string;
    completedRecordedAt?: string;
    completedProvenance?: string;
  };
  currentRun?: { id: string; number: string; executionId?: string };
  group?: {
    collectionId?: string;
    collectionIndex?: number;
    graphId?: string;
    graphIndex?: number;
  };
}
export interface Overview {
  active: string;
  awaitingExecution: string;
  running: string;
  evidenceAttention: string;
  terminal: Record<string, string>;
  missingCompletionTime: string;
  window: { from: string; to: string };
}
export type Target =
  import("../../../contracts/typescript/dashboard.generated").Target;
export interface Workload extends NamespaceRef {
  id: string;
  name?: string;
  kind: string;
  revision: string;
  createdAt: string;
  asOf: string;
  updatedAt?: string;
  phase?: string;
  outcome?: string;
  unsatisfiedPolicy?: string;
  arrayMode?: string;
  arrayPolicy?: string;
  totalChildren: string;
  counts: Record<string, string>;
  concurrency?: string;
  failurePolicy?: string;
  arrayId?: string;
}
export interface WorkloadCatalog {
  items: Workload[];
  total: string;
  totals: (NamespaceRef & { total: string; asOf: string })[];
}
export interface GraphNode {
  id: string;
  name?: string;
  index?: string;
  dependencyCounts?: Record<string, string>;
  job: Job;
  readiness?: string;
  disposition?: string;
  taskIndex?: string;
  dependencies?: {
    upstreamNodeId: string;
    predicate: string;
    status: string;
  }[];
}
export interface WorkloadDetail {
  workload: Workload;
  children: GraphNode[];
  total: string;
}
export interface GraphDependency {
  from: string;
  to: string;
  fromJobId: string;
  toJobId: string;
  predicate: string;
  outcomes: string[];
  upstreamPhase: string;
  upstreamOutcome?: string;
  state: string;
}
export interface GraphDependencies {
  items: GraphDependency[];
  total: string;
}
export interface GraphNeighborhood {
  centerId: string;
  nodes: GraphNode[];
  edges: GraphDependency[];
  totalNodes: string;
  totalEdges: string;
  omittedNodes: string;
  omittedEdges: string;
}
export interface Artifact {
  id: string;
  name: string;
  sizeBytes: string;
  checksum?: string;
  publishedAt?: string;
  availability: string;
}
export interface LogChunk {
  bytesBase64: string;
  executionId: string;
  text?: string;
  stream: string;
  runId?: string;
  startOffset: string;
  endOffset: string;
  nextCursor?: string;
  state: string;
  truncated?: boolean;
  capturedAt?: string;
}
export interface Citation {
  id: string;
  label: string;
  text?: string;
  startOffset?: string;
  endOffset?: string;
}
export interface Finding {
  id: string;
  severity: string;
  title: string;
  explanation: string;
  confidence?: number;
  confidenceBasis?: string;
  citations: Citation[];
  suggestions?: string[];
  generated?: boolean;
}
export interface Report {
  id: string;
  state: string;
  createdAt?: string;
  sourceRevision?: string;
  evidenceId?: string;
  analysisEvidenceId?: string;
  engineVersion?: string;
  disclosure?: string;
  findings: Finding[];
  missingEvidence: string[];
  warnings?: string[];
  retryAdvice?: string;
  message?: string;
}
export interface AlertRule {
  id: string;
  revision: string;
  name: string;
  enabled: boolean;
  scope: "watched_jobs" | "my_jobs" | "namespace_jobs";
  namespaces: NamespaceRef[];
  jobs: JobRef[];
  outcomeMode: "selected" | "all_terminal";
  outcomes: string[];
  activation?: { deploymentId: string; status: string }[];
}
export interface InboxItem {
  id: string;
  job: JobRef;
  outcome: string;
  eventAt: string;
  createdAt: string;
  read: boolean;
  matchedRules: string[];
  deliveryStatus?: string;
}
export interface Device {
  id: string;
  name: string;
  platform?: string;
  enabled: boolean;
  permission?: string;
  lastSeenAt?: string;
}
export const outcomes = [
  "success",
  "failure",
  "timed_out",
  "aborted",
  "lost",
  "cancelled",
] as const;
export const unsuccessful = ["failure", "timed_out", "aborted", "lost"];
