// @ts-nocheck
import React from "react";
import { render, screen } from "@testing-library/react-native";

it("scratch: which render", () => {
  console.log("RENDER SRC:", render.toString().slice(0, 400));
  console.log("RESOLVED:", require.resolve("@testing-library/react-native"));
});
