import { describe, expect, it } from "vitest";
import { DEFAULT_METHOD, METHODS, methodWord } from "./method";

describe("the compression method", () => {
  it("offers the five levels the record can carry, Normal in the middle", () => {
    expect(METHODS.map((m) => m.value)).toEqual(["store", "fastest", "normal", "better", "best"]);
    expect(METHODS.map((m) => m.word)).toEqual(["Store", "Fast", "Normal", "Better", "Best"]);
    expect(DEFAULT_METHOD).toBe("normal");
  });

  it("names each by its word, and reads an unset level as the default", () => {
    expect(methodWord("store")).toBe("Store");
    expect(methodWord("fastest")).toBe("Fast");
    expect(methodWord("best")).toBe("Best");
    expect(methodWord("")).toBe("Normal");
    expect(methodWord(undefined)).toBe("Normal");
  });
});
