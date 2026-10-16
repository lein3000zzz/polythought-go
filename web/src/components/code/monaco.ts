// Monaco, set up once: its features, the TypeScript language, and its workers, bundled as
// files of this origin, so the Content-Security-Policy needs only worker-src 'self' (A5).
// The editor's own worker is one Monaco cannot have the build bundle by itself. Imported
// only by MonacoEditor, which is loaded when a code editor first shows.
import 'monaco-editor/editor/editor.main'
import * as monaco from 'monaco-editor/editor/editor.api'
import {
  ModuleKind,
  ModuleResolutionKind,
  ScriptTarget,
  typescriptDefaults,
} from 'monaco-editor/languages/features/typescript/register'
import EditorWorker from 'monaco-editor/editor/editor.worker?worker'
import TypeScriptWorker from 'monaco-editor/language/typescript/ts.worker?worker'

self.MonacoEnvironment = {
  getWorker: (_id, label) =>
    label === 'typescript' || label === 'javascript' ? new TypeScriptWorker() : new EditorWorker(),
}

// As the sandbox runs code: modern JavaScript, no DOM, imports only from the SDK.
typescriptDefaults.setCompilerOptions({
  target: ScriptTarget.ESNext,
  module: ModuleKind.ESNext,
  moduleResolution: ModuleResolutionKind.NodeJs,
  lib: ['es2023'],
  strict: true,
  noEmit: true,
  allowNonTsExtensions: true,
})
typescriptDefaults.setEagerModelSync(true)

const sdkRoot = 'file:///node_modules/@polythought/sdk'

/**
 * Makes files the @polythought/sdk package code imports: the SDK's declarations and the
 * user's node types, from the server. Replaces what was there before.
 */
export function setSdk(files: readonly { path: string; content: string }[]) {
  typescriptDefaults.setExtraLibs([
    {
      filePath: `${sdkRoot}/package.json`,
      content: JSON.stringify({ name: '@polythought/sdk', types: 'index.d.ts' }),
    },
    ...files.map((f) => ({ filePath: `${sdkRoot}/${f.path}`, content: f.content })),
  ])
}

export { monaco }
