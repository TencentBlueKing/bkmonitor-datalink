// @vitest-environment node
import { randomUUID } from "node:crypto";
import mysql from "mysql2/promise";
import { expect, it } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { MysqlConnector } from "./mysql.js";
import { strategyLabel } from "./strategy-alerts.js";

const url = process.env.LINKD_TEST_MYSQL_URL;
// 需专用测试实例的建库权限；不使用或修改实例中已有数据库。
it.skipIf(!url)(
  "reads active strategy identities from real MySQL JSON and binary columns",
  async () => {
    const database = "linkd_strategy_test_" + randomUUID().replaceAll("-", "");
    const admin = await mysql.createConnection(url!);
    let connector: MysqlConnector | undefined;
    let created = false;
    try {
      await admin.query(`CREATE DATABASE ${database}`);
      created = true;
      await admin.query(
        `CREATE TABLE ${database}.linkd_alerts (bk_tenant_id VARBINARY(256), alert_id VARBINARY(256), event_source_id VARBINARY(256), fingerprint VARBINARY(256), status VARCHAR(32), payload JSON)`,
      );
      for (const [id, tenant, source, status, strategy] of [
        ["1", "tenant", "a", "active", 123],
        ["2", "tenant", "b", "active", "00123"],
        ["3", "another", "a", "active", 123],
        ["4", "tenant", "a", "closed", 123],
      ]) {
        await admin.query(
          `INSERT INTO ${database}.linkd_alerts VALUES (?, ?, ?, ?, ?, ?)`,
          [
            tenant,
            id,
            source,
            "fp-" + id,
            status,
            JSON.stringify({ labels: { strategy_id: strategy } }),
          ],
        );
      }
      const parsed = new URL(url!);
      const config = {
        server: { host: "127.0.0.1", port: 4399 },
        query: {
          timeoutMilliseconds: 3000,
          defaultRangeSeconds: 3600,
          maxRangeSeconds: 86400,
          defaultLimit: 50,
          maxLimit: 200,
        },
        mysql: {
          host: parsed.hostname,
          port: Number(parsed.port || "3306"),
          database,
          username: decodeURIComponent(parsed.username),
          password: decodeURIComponent(parsed.password),
          connectionLimit: 1,
        },
        entities: { alerts: "mysql", events: "mysql", alertLogs: "mysql" },
      } satisfies ConsoleConfig;
      connector = new MysqlConnector(config);
      const rows = await connector.readStrategyAlerts(
        "tenant",
        ["a", "b"],
        "123",
        5001,
      );
      expect(
        rows.map((row) => [
          row.alert_id,
          row.fingerprint,
          strategyLabel(row.labels?.strategy_id),
        ]),
      ).toEqual([["1", "fp-1", "123"]]);
      expect(
        await connector.readStrategyAlerts("TENANT", ["a", "b"], "123", 5001),
      ).toEqual([]);
      const exact = await connector.readStrategyAlerts(
        "tenant",
        ["a", "b"],
        "00123",
        5001,
      );
      expect(exact.map((row) => row.alert_id)).toEqual(["2"]);
      const discovered = [];
      for await (const page of connector.scanActiveStrategyAlerts(
        ["a", "b"],
        AbortSignal.timeout(5000),
      ))
        discovered.push(...page);
      expect(discovered.map((row) => row.alert_id).sort()).toEqual([
        "1",
        "2",
        "3",
      ]);
      const values = Array.from({ length: 1005 }, (_, i) => [
        "tenant",
        `page-${String(i).padStart(5, "0")}`,
        "paged",
        `fp-${i}`,
        "active",
        JSON.stringify({ labels: { strategy_id: "bulk" } }),
      ]);
      await admin.query(`INSERT INTO ${database}.linkd_alerts VALUES ?`, [
        values,
      ]);
      const paged = [];
      for await (const page of connector.scanActiveStrategyAlerts(
        ["paged"],
        AbortSignal.timeout(5000),
      ))
        paged.push(page);
      expect(paged.map((page) => page.length)).toEqual([1000, 5]);
      expect(new Set(paged.flat().map((row) => row.alert_id)).size).toBe(1005);
    } finally {
      await connector?.close();
      if (created) await admin.query(`DROP DATABASE ${database}`);
      await admin.end();
    }
  },
  20000,
);
