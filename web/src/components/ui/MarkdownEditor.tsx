import { useEditor, EditorContent, ReactNodeViewRenderer, NodeViewWrapper, NodeViewContent } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Color } from '@tiptap/extension-color';
import { TextStyle } from '@tiptap/extension-text-style';
import Highlight from '@tiptap/extension-highlight';
import { Markdown } from 'tiptap-markdown';
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight';
import { createLowlight } from 'lowlight';
import javascript from 'highlight.js/lib/languages/javascript';
import typescript from 'highlight.js/lib/languages/typescript';
import python from 'highlight.js/lib/languages/python';
import cpp from 'highlight.js/lib/languages/cpp';
import csharp from 'highlight.js/lib/languages/csharp';
import bash from 'highlight.js/lib/languages/bash';
import powershell from 'highlight.js/lib/languages/powershell';
import sql from 'highlight.js/lib/languages/sql';
import json from 'highlight.js/lib/languages/json';
import yaml from 'highlight.js/lib/languages/yaml';
import go from 'highlight.js/lib/languages/go';
import rust from 'highlight.js/lib/languages/rust';
import { useState, useRef, useEffect } from 'react';
import {
  Bold, Italic, Strikethrough,
  Heading1, Heading2, Heading3,
  Code, Code2, Quote, List, ListOrdered,
  Minus, FileCode2, ChevronLeft,
  Highlighter, Palette,
} from 'lucide-react';
import { cn } from '../../lib/utils';

// ─── Lowlight setup ─────────────────────────────────────────────────────────

const lowlight = createLowlight();
lowlight.register('javascript', javascript);
lowlight.register('js', javascript);
lowlight.register('typescript', typescript);
lowlight.register('ts', typescript);
lowlight.register('python', python);
lowlight.register('py', python);
lowlight.register('cpp', cpp);
lowlight.register('c++', cpp);
lowlight.register('csharp', csharp);
lowlight.register('cs', csharp);
lowlight.register('bash', bash);
lowlight.register('sh', bash);
lowlight.register('powershell', powershell);
lowlight.register('pwsh', powershell);
lowlight.register('sql', sql);
lowlight.register('json', json);
lowlight.register('yaml', yaml);
lowlight.register('yml', yaml);
lowlight.register('go', go);
lowlight.register('rust', rust);

const LANGUAGES = [
  { value: '',           label: 'Plain text',   color: '#718096' },
  { value: 'javascript', label: 'JavaScript',   color: '#f0db4f' },
  { value: 'typescript', label: 'TypeScript',   color: '#3178c6' },
  { value: 'python',     label: 'Python',       color: '#3776ab' },
  { value: 'cpp',        label: 'C++',          color: '#00599c' },
  { value: 'csharp',     label: 'C#',           color: '#9b59b6' },
  { value: 'go',         label: 'Go',           color: '#00acd7' },
  { value: 'rust',       label: 'Rust',         color: '#ce422b' },
  { value: 'bash',       label: 'Bash / Shell', color: '#4eaa25' },
  { value: 'powershell', label: 'PowerShell',   color: '#5391fe' },
  { value: 'sql',        label: 'SQL',          color: '#e38c00' },
  { value: 'json',       label: 'JSON',         color: '#8bc34a' },
  { value: 'yaml',       label: 'YAML',         color: '#cb171e' },
];

// ─── Language picker (GUI dropdown for code blocks) ──────────────────────────

function LanguagePicker({ language, onChange }: { language: string; onChange: (v: string) => void }) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const current = LANGUAGES.find(l => l.value === language) ?? LANGUAGES[0];
  const filtered = LANGUAGES.filter(l =>
    l.label.toLowerCase().includes(search.toLowerCase())
  );

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  useEffect(() => {
    if (open) {
      setSearch('');
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  return (
    <div ref={ref} className="relative" contentEditable={false}>
      {/* Trigger badge */}
      <button
        type="button"
        onMouseDown={e => { e.preventDefault(); setOpen(o => !o); }}
        className="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-semibold transition-colors hover:bg-white/10"
        style={{ color: current.color }}
      >
        <span
          className="h-2 w-2 rounded-full shrink-0"
          style={{ background: current.color }}
        />
        {current.label}
        <svg className={`h-3 w-3 transition-transform ${open ? 'rotate-180' : ''}`} viewBox="0 0 12 12" fill="currentColor">
          <path d="M2 4l4 4 4-4" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round"/>
        </svg>
      </button>

      {/* Dropdown panel */}
      {open && (
        <div
          className="absolute left-0 top-full z-50 mt-1 rounded-lg shadow-2xl overflow-hidden"
          style={{ background: '#1e2235', border: '1px solid #3d4260', width: '220px' }}
        >
          {/* Search */}
          <div className="p-2 border-b" style={{ borderColor: '#3d4260' }}>
            <input
              ref={inputRef}
              type="text"
              placeholder="Search language…"
              value={search}
              onChange={e => setSearch(e.target.value)}
              className="w-full rounded-md px-2.5 py-1.5 text-xs focus:outline-none"
              style={{ background: '#141620', color: '#e8eaf0', border: '1px solid #3d4260' }}
              onMouseDown={e => e.stopPropagation()}
            />
          </div>

          {/* Options */}
          <div className="max-h-52 overflow-y-auto p-1.5">
            {filtered.length === 0 && (
              <p className="px-3 py-2 text-xs" style={{ color: '#6b7194' }}>No match</p>
            )}
            {filtered.map(l => (
              <button
                key={l.value}
                type="button"
                onMouseDown={e => {
                  e.preventDefault();
                  onChange(l.value);
                  setOpen(false);
                }}
                className="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-xs transition-colors hover:bg-white/8"
                style={{
                  color: l.value === language ? l.color : '#c9d1e8',
                  background: l.value === language ? 'rgba(255,255,255,0.06)' : undefined,
                  fontWeight: l.value === language ? 600 : 400,
                }}
              >
                <span className="h-2.5 w-2.5 rounded-full shrink-0" style={{ background: l.color }} />
                {l.label}
                {l.value === language && (
                  <svg className="ml-auto h-3 w-3" viewBox="0 0 12 12" fill="currentColor" style={{ color: l.color }}>
                    <path d="M2 6l3 3 5-5" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
                  </svg>
                )}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Code block node view ────────────────────────────────────────────────────

function CodeBlockView({ node, updateAttributes }: any) {
  const language = node.attrs.language || '';
  return (
    <NodeViewWrapper className="relative my-3" style={{ userSelect: 'none' }}>
      <div className="overflow-hidden rounded-md" style={{ border: '1px solid #3d4260' }}>
        {/* Header */}
        <div className="flex items-center justify-between px-3 py-1.5" style={{ background: '#141620', borderBottom: '1px solid #3d4260' }}>
          <LanguagePicker
            language={language}
            onChange={lang => updateAttributes({ language: lang })}
          />
        </div>
        {/* Code area */}
        <pre className="overflow-x-auto p-4" style={{ background: '#1a1d27', margin: 0 }}>
          <NodeViewContent as="div" className={language ? `language-${language} hljs` : ''} style={{ color: '#e8eaf0', fontFamily: 'monospace', fontSize: '0.875rem', whiteSpace: 'pre', userSelect: 'text' }} />
        </pre>
        {/* Footer — marks the end of the block */}
        <div style={{ background: '#141620', borderTop: '1px solid #3d4260', height: '6px' }} />
      </div>
    </NodeViewWrapper>
  );
}

// ─── Types ──────────────────────────────────────────────────────────────────

interface MarkdownEditorProps {
  value: string;
  /** Called with the latest markdown string on every content change. */
  onChange: (value: string) => void;
  disabled?: boolean;
}

// ─── Color swatches ─────────────────────────────────────────────────────────

const TEXT_COLORS = [
  { label: 'Default', color: '' },
  { label: 'Red',     color: '#e53e3e' },
  { label: 'Orange',  color: '#dd6b20' },
  { label: 'Yellow',  color: '#d69e2e' },
  { label: 'Green',   color: '#38a169' },
  { label: 'Blue',    color: '#3182ce' },
  { label: 'Purple',  color: '#805ad5' },
  { label: 'Pink',    color: '#d53f8c' },
  { label: 'Gray',    color: '#718096' },
];

const HIGHLIGHT_COLORS = [
  { label: 'None',   color: '' },
  { label: 'Yellow', color: '#fefcbf' },
  { label: 'Green',  color: '#c6f6d5' },
  { label: 'Blue',   color: '#bee3f8' },
  { label: 'Pink',   color: '#fed7e2' },
  { label: 'Purple', color: '#e9d8fd' },
  { label: 'Orange', color: '#feebc8' },
];

function ColorPicker({
  colors, onSelect, icon: Icon, title,
}: {
  colors: { label: string; color: string }[];
  onSelect: (color: string) => void;
  icon: React.ComponentType<{ className?: string }>;
  title: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  return (
    <div ref={ref} className="relative">
      <button type="button" title={title} onClick={() => setOpen(o => !o)}
        style={{ color: '#c9d1e8' }}
        className="rounded-md p-1.5 transition-colors hover:bg-white/10"
      >
        <Icon className="h-4 w-4" />
      </button>
      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 grid grid-cols-4 gap-1 rounded-lg p-2 shadow-xl"
          style={{ background: '#1e2235', border: '1px solid #3d4260', minWidth: '9rem' }}
        >
          {colors.map(({ label, color }) => (
            <button key={label} type="button" title={label}
              onClick={() => { onSelect(color); setOpen(false); }}
              className="h-6 w-6 rounded border border-white/10 transition-transform hover:scale-110"
              style={{ background: color || '#3d4260' }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Main component ──────────────────────────────────────────────────────────

/**
 * Obsidian-style wiki editor.
 * Default: live rendered editing with formatting toolbar.
 * "Markdown" button: toggle to raw markdown source.
 */
export function MarkdownEditor({ value, onChange, disabled }: MarkdownEditorProps) {
  const [showMarkdown, setShowMarkdown] = useState(false);
  // rawMarkdown mirrors editor content for the textarea view
  const rawRef = useRef(value);
  const [rawMarkdown, setRawMarkdown] = useState(value);

  const editor = useEditor({
    extensions: [
      StarterKit.configure({ codeBlock: false }),
      TextStyle,
      Color,
      Highlight.configure({ multicolor: true }),
      Markdown.configure({ html: true, tightLists: true, transformPastedText: true }),
      CodeBlockLowlight
        .extend({ addNodeView() { return ReactNodeViewRenderer(CodeBlockView); } })
        .configure({ lowlight }),
    ],
    content: value,
    editable: !disabled,
    onUpdate({ editor }) {
      const md: string = (editor as any).storage.markdown.getMarkdown();
      rawRef.current = md;
      setRawMarkdown(md);
      onChange(md);
    },
    editorProps: {
      attributes: {
        class: 'outline-none min-h-[22rem] p-4',
        style: 'color:#e8eaf0; cursor:text',
      },
    },
  });

  // When switching FROM markdown textarea BACK to rich editor, push edits in
  const prevShowMarkdown = useRef(showMarkdown);
  useEffect(() => {
    if (prevShowMarkdown.current && !showMarkdown && editor) {
      editor.commands.setContent(rawRef.current);
      onChange(rawRef.current);
    }
    prevShowMarkdown.current = showMarkdown;
  }, [showMarkdown, editor]);

  // ── Toolbar helpers ──────────────────────────────────────────────────────

  const sep = <div className="mx-1 h-4 w-px shrink-0" style={{ background: '#3d4260' }} />;

  const btn = (
    title: string,
    active: boolean,
    onClick: () => void,
    Icon: React.ComponentType<{ className?: string }>,
  ) => (
    <button type="button" title={title} disabled={disabled || showMarkdown} onClick={onClick}
      className="rounded-md p-1.5 transition-colors disabled:opacity-30"
      style={{ color: active ? '#4A90D9' : '#c9d1e8', background: active ? 'rgba(74,144,217,0.15)' : undefined }}
    >
      <Icon className="h-4 w-4" />
    </button>
  );

  if (!editor) return null;

  return (
    <div className="overflow-hidden rounded-lg" style={{ border: '2px solid #4A90D9' }}>

      {/* ── Toolbar ─────────────────────────────────────────────────────── */}
      <div className="flex flex-wrap items-center gap-0.5 px-2 py-1.5"
        style={{ background: '#1e2235', borderBottom: '2px solid #4A90D9' }}
      >
        {btn('Heading 1', editor.isActive('heading', { level: 1 }), () => editor.chain().focus().toggleHeading({ level: 1 }).run(), Heading1)}
        {btn('Heading 2', editor.isActive('heading', { level: 2 }), () => editor.chain().focus().toggleHeading({ level: 2 }).run(), Heading2)}
        {btn('Heading 3', editor.isActive('heading', { level: 3 }), () => editor.chain().focus().toggleHeading({ level: 3 }).run(), Heading3)}
        {sep}
        {btn('Bold',          editor.isActive('bold'),          () => editor.chain().focus().toggleBold().run(),          Bold)}
        {btn('Italic',        editor.isActive('italic'),        () => editor.chain().focus().toggleItalic().run(),        Italic)}
        {btn('Strikethrough', editor.isActive('strike'),        () => editor.chain().focus().toggleStrike().run(),        Strikethrough)}
        {sep}
        {btn('Inline code', editor.isActive('code'),      () => editor.chain().focus().toggleCode().run(),      Code)}
        {btn('Code block',  editor.isActive('codeBlock'), () => editor.chain().focus().toggleCodeBlock().run(), Code2)}
        {btn('Blockquote',  editor.isActive('blockquote'), () => editor.chain().focus().toggleBlockquote().run(), Quote)}
        {sep}
        {btn('Bullet list',   editor.isActive('bulletList'),  () => editor.chain().focus().toggleBulletList().run(),  List)}
        {btn('Numbered list', editor.isActive('orderedList'), () => editor.chain().focus().toggleOrderedList().run(), ListOrdered)}
        {sep}
        {btn('Horizontal rule', false, () => editor.chain().focus().setHorizontalRule().run(), Minus)}
        {sep}

        <ColorPicker title="Text color" icon={Palette} colors={TEXT_COLORS}
          onSelect={color => color
            ? editor.chain().focus().setColor(color).run()
            : editor.chain().focus().unsetColor().run()
          }
        />
        <ColorPicker title="Highlight" icon={Highlighter} colors={HIGHLIGHT_COLORS}
          onSelect={color => color
            ? editor.chain().focus().setHighlight({ color }).run()
            : editor.chain().focus().unsetHighlight().run()
          }
        />

        <div className="flex-1" />

        <button type="button" disabled={disabled}
          onClick={() => setShowMarkdown(s => !s)}
          className={cn('flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-semibold')}
          style={showMarkdown
            ? { background: '#4A90D9', color: '#fff' }
            : { background: '#2a2d3a', color: '#8b91a8' }}
        >
          {showMarkdown
            ? <><ChevronLeft className="h-3.5 w-3.5" />Back to editor</>
            : <><FileCode2 className="h-3.5 w-3.5" />Markdown</>}
        </button>
      </div>

      {/* ── Content ─────────────────────────────────────────────────────── */}
      <div style={{ background: '#1a1d27' }}>
        {showMarkdown ? (
          <textarea
            value={rawMarkdown}
            onChange={e => {
              setRawMarkdown(e.target.value);
              rawRef.current = e.target.value;
            }}
            rows={20}
            disabled={disabled}
            placeholder="Markdown source…"
            className="w-full resize-none p-4 font-mono text-sm focus:outline-none"
            style={{ background: '#1a1d27', color: '#e8eaf0', display: 'block', minHeight: '22rem' }}
          />
        ) : (
          <div className={cn(
            '[&_.ProseMirror]:outline-none',
            '[&_.ProseMirror_h1]:text-3xl [&_.ProseMirror_h1]:font-bold [&_.ProseMirror_h1]:text-[#e8eaf0] [&_.ProseMirror_h1]:mb-3 [&_.ProseMirror_h1]:mt-2',
            '[&_.ProseMirror_h2]:text-2xl [&_.ProseMirror_h2]:font-bold [&_.ProseMirror_h2]:text-[#e8eaf0] [&_.ProseMirror_h2]:mb-2 [&_.ProseMirror_h2]:mt-2',
            '[&_.ProseMirror_h3]:text-xl  [&_.ProseMirror_h3]:font-semibold [&_.ProseMirror_h3]:text-[#e8eaf0] [&_.ProseMirror_h3]:mb-2',
            '[&_.ProseMirror_p]:text-[#c9d1e8] [&_.ProseMirror_p]:leading-relaxed [&_.ProseMirror_p]:mb-2',
            '[&_.ProseMirror_ul]:list-disc [&_.ProseMirror_ul]:pl-6 [&_.ProseMirror_ul]:text-[#c9d1e8] [&_.ProseMirror_ul]:mb-2',
            '[&_.ProseMirror_ol]:list-decimal [&_.ProseMirror_ol]:pl-6 [&_.ProseMirror_ol]:text-[#c9d1e8] [&_.ProseMirror_ol]:mb-2',
            '[&_.ProseMirror_li]:mb-1',
            '[&_.ProseMirror_code]:bg-[#242836] [&_.ProseMirror_code]:text-[#4A90D9] [&_.ProseMirror_code]:rounded [&_.ProseMirror_code]:px-1 [&_.ProseMirror_code]:text-sm',
            '[&_.ProseMirror_pre]:bg-[#242836] [&_.ProseMirror_pre]:rounded-lg [&_.ProseMirror_pre]:p-4 [&_.ProseMirror_pre]:my-3 [&_.ProseMirror_pre]:overflow-x-auto',
            '[&_.ProseMirror_blockquote]:border-l-4 [&_.ProseMirror_blockquote]:border-[#4A90D9] [&_.ProseMirror_blockquote]:pl-4 [&_.ProseMirror_blockquote]:text-[#8b91a8] [&_.ProseMirror_blockquote]:italic [&_.ProseMirror_blockquote]:my-3',
            '[&_.ProseMirror_hr]:border-[#3d4260] [&_.ProseMirror_hr]:my-4',
            '[&_.ProseMirror_strong]:text-[#e8eaf0] [&_.ProseMirror_strong]:font-bold',
          )}>
            <EditorContent editor={editor} />
          </div>
        )}
      </div>
    </div>
  );
}
