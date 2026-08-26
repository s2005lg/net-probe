import { readdir, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const limit = 512_000;
const assetsDir = fileURLToPath(new URL("../dist/assets/", import.meta.url));
const files = (await readdir(assetsDir)).filter((name) => name.endsWith(".js"));
if (files.length === 0) {
  throw new Error(`no JavaScript chunks found in ${assetsDir}`);
}

const chunks = await Promise.all(
  files.map(async (name) => ({
    name,
    bytes: (await stat(`${assetsDir}/${name}`)).size,
  })),
);
const oversized = chunks.filter(({ bytes }) => bytes > limit);
if (oversized.length > 0) {
  for (const { name, bytes } of oversized) {
    console.error(`${name}: ${bytes} bytes exceeds ${limit}-byte limit`);
  }
  process.exit(1);
}

console.log(`bundle check: PASS (${chunks.length} chunks, limit ${limit} bytes)`);
