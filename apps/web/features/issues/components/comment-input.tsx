"use client";

import { useRef, useState } from "react";
import { ArrowUp, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { RichTextEditor, type RichTextEditorRef } from "@/components/common/rich-text-editor";
import { FileUploadButton } from "@/components/common/file-upload-button";
import { useFileUpload } from "@/shared/hooks/use-file-upload";

interface CommentInputProps {
  issueId: string;
  onSubmit: (content: string, attachmentIds?: string[]) => Promise<void>;
}

function CommentInput({ issueId, onSubmit }: CommentInputProps) {
  const editorRef = useRef<RichTextEditorRef>(null);
  const attachmentIdsRef = useRef<string[]>([]);
  const [isEmpty, setIsEmpty] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const { uploadWithToast, uploading } = useFileUpload();

  const handleUpload = async (file: File) => {
    const result = await uploadWithToast(file, { issueId });
    if (result) attachmentIdsRef.current.push(result.id);
    return result;
  };

  const handleSubmit = async () => {
    const content = editorRef.current?.getMarkdown()?.replace(/(\n\s*)+$/, "").trim();
    if (!content || submitting) return;
    setSubmitting(true);
    try {
      const ids = attachmentIdsRef.current.length > 0 ? [...attachmentIdsRef.current] : undefined;
      await onSubmit(content, ids);
      editorRef.current?.clearContent();
      attachmentIdsRef.current = [];
      setIsEmpty(true);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="relative rounded-lg bg-card ring-1 ring-border">
      <div className="min-h-20 max-h-48 overflow-y-auto px-3 py-2 pb-8">
        <RichTextEditor
          ref={editorRef}
          placeholder="Leave a comment..."
          onUpdate={(md) => setIsEmpty(!md.trim())}
          onSubmit={handleSubmit}
          onUploadFile={handleUpload}
          debounceMs={100}
        />
      </div>
      <div className="absolute bottom-1 right-1.5 flex items-center gap-1">
        <FileUploadButton
          size="sm"
          onUpload={handleUpload}
          onInsert={(result, isImage) =>
            editorRef.current?.insertFile(result.filename, result.link, isImage)
          }
          disabled={uploading}
        />
        <Button
          size="icon-xs"
          disabled={isEmpty || submitting}
          onClick={handleSubmit}
        >
          {submitting ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <ArrowUp className="h-3.5 w-3.5" />
          )}
        </Button>
      </div>
    </div>
  );
}

export { CommentInput };
