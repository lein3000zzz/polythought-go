import { workflow } from "@polythought/workflow"

export default workflow("Odd \"names\" and \\ slashes", (w) => {
  const manual = w.trigger("manual", "trigger.manual", {}, {
    notes: `Starts it
by hand`,
  })
  const code = w.node("code", "code.filter", {
    code: `export function keep(item) {\r
  return \`\${item.a}\` !== "\\n" // \${not}
}
`,
  })
  const ask = w.node("ask", "telegram.ask", {
    message: "Buy? ✓   line sep, \u0001 control, \ttab,  del, emoji 🚀",
    buttons: ["Yes", "No"],
  })
  const empty = w.node("empty", "logic.unknown")
  const slashes = w.node("slashes", "output.log", {
    message: "back\\slash, \"quotes\", été, `tick`, ${not a template}",
  })

  manual.to(code)
  code.to(ask)
  ask.port("Yes").to(empty)
  ghost.to(slashes.input("in put"))
})
