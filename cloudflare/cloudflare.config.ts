import { defineConfig, triggers } from "cf/config";

const { CF_ACCOUNT_ID, ZONES } = process.env;
if (!CF_ACCOUNT_ID || !ZONES) {
  throw new Error("The shield deploys with CF_ACCOUNT_ID and ZONES set");
}

export default defineConfig({
  accountId: CF_ACCOUNT_ID,
  worker: {
    name: "citrine-shield",
    entrypoint: "./src/index.js",
    compatibilityDate: "2026-11-01",
    workersDev: false,
    observability: { enabled: true },
    triggers: ZONES.split(",").flatMap((zone) => [
      triggers.fetch({ pattern: `${zone}/*`, zone }),
      triggers.fetch({ pattern: `*.${zone}/*`, zone }),
    ]),
  },
});
