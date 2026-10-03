# Web transport integration notes

Web uses same-origin `/api/v1` requests with cookies, `cache: no-store`, redirect rejection, `X-CSRF-Token` on mutations, `If-Match` for revisions, and `Idempotency-Key` for new report/rule requests. No bearer credentials or API responses are stored in browser storage. See `src/lib/api.ts` for bootstrap/job normalization; `src/lib/models.ts` defines application/secondary DTOs.

The authoritative bootstrap/jobs/overview shape follows the root-owned OpenAPI. `bootstrap.fixtureMode: true` displays an unavoidable synthetic-environment banner. Both clients use scope JSON: `[{deploymentId,namespaceId}]`. No client-side fixture fallback exists.

## Secondary contract field sets

All list responses use `{items, nextCursor?, completeness, sources, fetchedAt}`. `sources` contains `{deploymentId,namespaceId,status,asOf?,fetchedAt,message?}`. All identifiers are strings. Counts, byte positions, generations, revisions are decimal strings. Endpoints accept `limit=50` and opaque `cursor`; source-scoped catalogs also accept `scope`.

- `GET /workloads/{kind}`; kind `collection|array|graph`: standard page plus `total` and `totals:[{deploymentId,namespaceId,total,asOf}]`. Aggregate total is explicitly a subtotal when completeness is partial. Items `{deploymentId,namespaceId,id,name?,kind,revision,createdAt,updatedAt?,asOf,totalChildren,counts:Record<string,string>,concurrency?,failurePolicy?,unsatisfiedPolicy?,phase?,outcome?,arrayMode?,arrayPolicy?,arrayId?}`. Collection catalogs include all collections; the array catalog restricts to source-reported `slurm-array` mode. Wrapper counts never add jobs to the Overview.
- `GET /deployments/:d/namespaces/:n/workloads/:kind/:id`: `{workload,children,total,nextCursor?,completeness,sources,fetchedAt}`. `workload.asOf` belongs to the complete summary; `sources[].asOf` belongs to the child-page observation. Both are live observations and can differ.
- `GET <workload>/children?limit=50&cursor=...`: standard page plus `total`. Child `{id,name?,index,job:<canonical Job DTO>,disposition?,taskIndex?,dependencyCounts?:{total,satisfied,waiting,unsatisfied}}`. The immutable child job UUID is its `id`. `index`, sparse `taskIndex` and complete incoming dependency counts are decimal strings; zero is preserved. No readiness or unbounded dependency list is inferred from a node page.
- `GET <graph>/dependencies?limit=100&nodeId=...&direction=incoming|outgoing&cursor=...`: standard page plus `total`, with `{from,to,fromJobId,toJobId,predicate,outcomes:string[],upstreamPhase,upstreamOutcome?,state}`. Omit nodeId/direction to browse every graph edge. Direction requires nodeId. Limit maximum 500.
- `GET <graph>/neighborhood?nodeId=...&maxNodes=50&maxEdges=100`: `{centerId,nodes:WorkloadChild[],edges:GraphEdge[],totalNodes,totalEdges,omittedNodes,omittedEdges,completeness,sources,fetchedAt}`. Node/edge totals count the full one-hop induced neighborhood, including omitted data; this is not the whole graph. Limits maximum 200 nodes / 500 edges. Only edges whose endpoints were returned may appear. Node dependency counts still describe all incoming edges, including those outside the neighborhood. The web constructs visual parent references only from actual returned edges and offers equivalent node/dependency lists.
- `GET /targets`: items `{deploymentId,namespaceId,targetId,name,state,provider?,backend?,generation?,capabilities:string[]}`. State is configured state only.
- `GET <job>/artifacts`: items `{id,name,sizeBytes,checksum?,publishedAt?,availability}`. No download links.
- `GET <job>/logs?stream=stdout|stderr&limitBytes=262144&cursor=...`: `{bytesBase64,executionId,stream,runId?,startOffset,endOffset,nextCursor?,state,truncated?,capturedAt?}`. Raw bytes are decoded incrementally; byte lengths/contiguity checked, run/execution changes do not append. Stop state `complete`; missing/corrupt cases remain errors, never empty successful content. Root/native accepted base64 after initial text proposal.
- `GET <job>/reports`: items `{id,state,createdAt?,sourceRevision?,evidenceId?,analysisEvidenceId?,engineVersion?,disclosure?,findings:[],missingEvidence:string[],warnings?:string[],retryAdvice?,message?}`. State `absent|queued|collecting|analyzing|ready|failed|outdated`.
- Finding `{id,severity,title,explanation,confidence?,confidenceBasis?,citations:[{id,label}],suggestions?:string[],generated?:boolean}`.
- `POST <job>/reports` body `{includeLogTail:boolean,runId?}`; 202 task/result. UI refreshes report list. No provider requests or execution controls.
- `GET <job>/reports/:reportId/citations/:citationId`: `{id,label,text,startOffset?,endOffset?}` exact validated immutable evidence, server authorization required.
- `GET /rules`: items `{id,revision,name,enabled,scope,namespaces:NamespaceRef[],jobs:JobRef[],outcomeMode,outcomes:string[],activation?:[{deploymentId,status}]}`. Scope `watched_jobs|my_jobs|namespace_jobs`; outcomeMode `selected|all_terminal`. JobRef uses `jobId` (canonical job DTO separately uses `id`).
- `POST /rules`: above without id/revision; `PUT /rules/:id` revision-checked full replacement; `DELETE /rules/:id` reserved for removal (UI currently supports editing/disabling). Explicit namespace selections may span deployments. Watched jobs may be any visible member's jobs.
- `GET /inbox?unread=true`: items `{id,job:JobRef,outcome,eventAt,createdAt,read,matchedRules:string[],deliveryStatus?}`. `PATCH /inbox/:id` body `{read:boolean}`.
- `GET /devices`: items `{id,name,platform?,enabled,permission?,lastSeenAt?}`. `PATCH /devices/:id` body `{enabled:boolean}`. Device token registration belongs to native app; web does not accept or display APNs tokens.
- `PUT /preferences`: `{revision,timezone,appearance,refreshSeconds}` with If-Match; existing root bootstrap model. Save re-fetches bootstrap.
- `POST /auth/logout`: current session CSRF header; server clears cookie/revokes session.

## Query refinements

Overview: `windowHours=24|168`; requested half-open `from`/`to` supported for explicit drill-down. Jobs filters `phase`, `outcome`, `owner=me`, `jobId`, `from`, `to`, `attention=true`; aggregate presets `phase=active` and `phase=awaiting` are requests for the documented sets, not new lifecycle enum values.

Web displays unavailable rather than zero for null summary counts. Unknown enum values stay readable. Source failure returns explicit partial metadata. Resource-not-found/authorization failures clear cached sensitive view data.
