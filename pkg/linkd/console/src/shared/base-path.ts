/** normalizeBasePath 将根路径归一为空前缀，拒绝 URL、转义和不明确的路径段。 */
export function normalizeBasePath(value = "/"): string {
  if (value === "" || value === "/") return "";
  if (value.length > 256 || !/^(\/[A-Za-z0-9_-]+)+$/.test(value)) {
    throw new Error(
      "Console base path must be / or slash-separated letters, digits, underscores and hyphens (at most 256 characters, no trailing slash)",
    );
  }
  return value;
}
