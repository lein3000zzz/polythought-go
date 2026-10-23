import { workflow } from "@polythought/workflow"

export default workflow("Copy", (w) => {
  const copy = w.node("copy", "test.copy", {
    traders: [
      { address: "0x5f3c00000000000000000000000000000000a1b2", label: "Alice" },
      { address: "0x91d000000000000000000000000000000000c7e4", label: "Bob", on: false },
    ],
  })
  const mixed = w.node("mixed", "test.copy", {
    traders: [{ label: "Alone" }, "not a row"],
    empty: [],
  })
})
