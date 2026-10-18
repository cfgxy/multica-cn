import remarkGfm from "remark-gfm";

type ProcessorData = Record<string, unknown[] | undefined>;
type ProcessorLike = { data(): ProcessorData };

const sharedData: ProcessorData = {};

// Build the plugin's fixed extensions once; each processor keeps its own arrays.
Reflect.apply(remarkGfm, { data: () => sharedData }, [{ singleTilde: false }]);

export function remarkGfmWithSharedExtensions(this: unknown): void {
  const data = (this as ProcessorLike).data();
  for (const key of Object.keys(sharedData)) {
    const target = data[key] ?? (data[key] = []);
    target.push(...(sharedData[key] ?? []));
  }
}
