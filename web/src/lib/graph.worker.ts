/// <reference lib="webworker" />
import { layoutGraph, type LayoutNode } from "./graphLayout";
self.onmessage = (event: MessageEvent<{ nodes: LayoutNode[] }>) => {
  self.postMessage(layoutGraph(event.data.nodes));
};
