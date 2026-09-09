/** consoleBasePath 读取服务端注入的路径，深层页面刷新也不依赖当前 URL 推导前缀。 */
export function consoleBasePath(): string {
  return (
    document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") ??
    ""
  );
}

/** consoleURL 为 Console 内部 API 请求添加与浏览器路由一致的前缀。 */
export function consoleURL(path: string): string {
  return `${consoleBasePath()}${path}`;
}
