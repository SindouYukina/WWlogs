import emblem from "../brand/header_emblem.png"

/** The product emblem beside the name in a window header. It is decoration: the name next to it carries the meaning. */
export function BrandEmblem() {
  return <img className="ww-brand-emblem" src={emblem} alt="" width={30} height={30} decoding="async" draggable={false} />
}
