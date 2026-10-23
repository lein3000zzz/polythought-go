import { workflow } from "@polythought/workflow"

export default workflow("Values", (w) => {
  const v = w.node("v", "output.log", {
    message: "numbers and keys",
    "2": "two",
    "10": "ten",
    "4294967294": "biggest",
    "": 4,
    $ok: 1,
    "01": "not an index",
    "1st": 3,
    "4294967295": "too big",
    _ok: 2,
    a: "ay",
    b: "bee",
    flags: [true, false, null],
    nested: { deep: [1, 2.5, null, true, { ["__proto__"]: 1, "with-dash": "x" }] },
    numbers: [0, 0, 1, -2.5, 0.1, 1e+21, 100000000000000000000, 123456789012, 1e-7, 0.000001, 1.5e+300, -1e-300, 9007199254740992],
    "with space": 6,
    ünïcode: 5,
    אב: 7,
  })
})
