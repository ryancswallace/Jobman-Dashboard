export interface LayoutNode {
  id: string;
  parents: string[];
}

// Presentation only: bounded layout never computes execution readiness.
export function layoutGraph(input: LayoutNode[]) {
  const nodes = input.slice(0, 200),
    ids = new Set(nodes.map((n) => n.id)),
    levels = new Map<string, number>();
  for (let i = 0; i < nodes.length; i++) {
    let changed = false;
    for (const node of nodes) {
      if (levels.has(node.id)) continue;
      const parents = node.parents.filter((p) => ids.has(p));
      if (parents.every((p) => levels.has(p))) {
        levels.set(
          node.id,
          parents.length
            ? Math.max(...parents.map((p) => levels.get(p)!)) + 1
            : 0,
        );
        changed = true;
      }
    }
    if (!changed) break;
  }
  const rows = new Map<number, number>();
  const positions = nodes.map((node) => {
    const level = levels.get(node.id) ?? 0,
      row = rows.get(level) ?? 0;
    rows.set(level, row + 1);
    return { id: node.id, x: level * 250 + 30, y: row * 105 + 30 };
  });
  return {
    positions,
    width: Math.max(550, ...positions.map((p) => p.x + 220)),
    height: Math.max(180, ...positions.map((p) => p.y + 85)),
  };
}
