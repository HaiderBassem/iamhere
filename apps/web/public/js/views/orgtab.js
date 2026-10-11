// Dispatches /#/o/:org/(team|places|settings) to its view.
export default async function orgtab(ctx) {
  const mod = await import(`./${ctx.params[1]}.js`);
  return mod.default(ctx);
}
