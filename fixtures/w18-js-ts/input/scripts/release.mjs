export function release(tag) {
  try {
    return tag.trim();
  } catch (err) {}
}
