import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { z } from "zod";

const buildInfoSchema = z.object({
  version: z.string().min(1),
  git_commit: z.string().min(1),
});

/** 读取镜像构建时写入的版本；运行时环境变量不能覆盖这份来源记录。 */
export function readBuildInfo(
  path: string = resolve(import.meta.dirname, "../../build-info.json"),
) {
  let text: string;
  try {
    text = readFileSync(path, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") {
      return { version: "dev", git_commit: "unknown" };
    }
    throw error;
  }
  return buildInfoSchema.parse(JSON.parse(text));
}

export function formatBuildInfo(
  info: ReturnType<typeof readBuildInfo>,
): string {
  return `version: ${info.version}\ngit_commit: ${info.git_commit}\n`;
}
