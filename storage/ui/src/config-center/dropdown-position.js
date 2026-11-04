const DEFAULT_MAX_HEIGHT = 224;
const DEFAULT_VIEWPORT_MARGIN = 12;
const DEFAULT_GAP = 4;

export function calculateDropdownPosition({
  triggerTop,
  triggerBottom,
  viewportHeight,
  menuHeight,
  maxHeight = DEFAULT_MAX_HEIGHT,
  viewportMargin = DEFAULT_VIEWPORT_MARGIN,
  gap = DEFAULT_GAP,
}) {
  const spaceBelow = Math.max(0, viewportHeight - triggerBottom - viewportMargin - gap);
  const spaceAbove = Math.max(0, triggerTop - viewportMargin - gap);
  const desiredHeight = Math.min(menuHeight, maxHeight);
  const opensAbove = spaceBelow < desiredHeight && spaceAbove > spaceBelow;
  const availableHeight = opensAbove ? spaceAbove : spaceBelow;

  return {
    opensAbove,
    maxHeight: Math.max(0, Math.min(maxHeight, Math.floor(availableHeight))),
  };
}
