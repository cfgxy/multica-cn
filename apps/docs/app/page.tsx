import { source } from "@/lib/source";
import { DocsPage, DocsBody } from "fumadocs-ui/page";
import { notFound } from "next/navigation";
import defaultMdxComponents from "fumadocs-ui/mdx";
import type { Metadata } from "next";
import { DocsHero } from "@/components/hero";
import { Byline, NumberedCards, NumberedCard, NumberedSteps, Step } from "@/components/editorial";

export default function Page() {
  const page = source.getPage([]);
  if (!page) notFound();

  const MDX = page.data.body;

  return (
    <DocsPage toc={page.data.toc}>
      <DocsHero
        eyebrow="Multica 文档"
        title={
          <>
            人与智能体，<em className="font-medium not-italic text-[var(--primary)]">共处一方。</em>
          </>
        }
        subtitle={page.data.description}
      />
      <Byline items={["开始使用", "2026 年 4 月更新", "阅读约 6 分钟"]} />
      <DocsBody>
        <MDX
          components={{
            ...defaultMdxComponents,
            NumberedCards,
            NumberedCard,
            NumberedSteps,
            Step,
          }}
        />
      </DocsBody>
    </DocsPage>
  );
}

export function generateMetadata(): Metadata {
  const page = source.getPage([]);
  if (!page) notFound();

  return {
    title: page.data.title,
    description: page.data.description,
  };
}
