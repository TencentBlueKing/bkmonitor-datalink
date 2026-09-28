import { createHmac } from "node:crypto";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { internalTokenHeaders } from "./internal-token.js";

const fixture = JSON.parse(
  readFileSync("../internal/internaltoken/testdata/interop.json", "utf8"),
) as {
  secret_key: string;
  username: string;
  unix: number;
  python_timed: string;
  go_timed: string;
};

describe("Kingeye internal JWT", () => {
  it("matches PyJWT and verifies the Go signing fixture", () => {
    const credentials = {
      secretKey: fixture.secret_key,
      username: fixture.username,
    };
    const headers = internalTokenHeaders(
      credentials,
      () => fixture.unix * 1000,
    );
    expect(headers).toEqual({
      "Internal-Token": `Bearer ${fixture.python_timed}`,
    });
    const [header, payload, signature] = fixture.go_timed.split(".");
    expect(
      createHmac("sha256", fixture.secret_key)
        .update(`${header}.${payload}`)
        .digest("base64url"),
    ).toBe(signature);
    expect(JSON.parse(Buffer.from(payload, "base64url").toString())).toEqual({
      username: fixture.username,
      iat: fixture.unix,
      exp: fixture.unix + 300,
    });
    const later = internalTokenHeaders(
      credentials,
      () => (fixture.unix + 300) * 1000,
    );
    expect(later).not.toEqual(headers);
    const claims = JSON.parse(
      Buffer.from(
        later["Internal-Token"].split(".")[1],
        "base64url",
      ).toString(),
    );
    expect(claims.exp).toBe(fixture.unix + 600);
  });
  it.each([
    { secretKey: "", username: "admin" },
    { secretKey: "  ", username: "admin" },
    { secretKey: "secret", username: "  " },
    { secretKey: "secret", username: "x".repeat(8192) },
  ])("rejects unusable credentials", (credentials) => {
    expect(() => internalTokenHeaders(credentials)).toThrow();
  });
});
