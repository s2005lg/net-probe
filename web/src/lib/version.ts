export type UpgradeStatus = "upgradable" | "latest" | "unknown";

function normalize(version: string): string {
  return version.trim().replace(/^[vV]+/, "");
}

function numericSegments(version: string): number[] {
  return normalize(version)
    .split(".")
    .map((part) => {
      const parsed = Number.parseInt(part, 10);
      return Number.isFinite(parsed) ? parsed : 0;
    });
}

export function upgradeStatus(current: string, latest: string): UpgradeStatus {
  if (!current || !latest) {
    return "unknown";
  }

  const currentNormalized = normalize(current);
  const latestNormalized = normalize(latest);
  if (currentNormalized === latestNormalized) {
    return "latest";
  }

  const currentSegments = numericSegments(current);
  const latestSegments = numericSegments(latest);
  const length = Math.max(currentSegments.length, latestSegments.length);

  for (let i = 0; i < length; i += 1) {
    const currentPart = currentSegments[i] ?? 0;
    const latestPart = latestSegments[i] ?? 0;
    if (currentPart < latestPart) {
      return "upgradable";
    }
    if (currentPart > latestPart) {
      return "latest";
    }
  }

  return "latest";
}
