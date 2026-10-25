"use client";

import type { XamanPending } from "@/lib/xaman";

type Props = {
  pending: XamanPending;
  title?: string;
  onCancel: () => void;
};

export function XamanQr({
  pending,
  title = "Scan with Xaman to sign",
  onCancel,
}: Props) {
  return (
    <div className="xaman-qr">
      <p className="xaman-qr__title">{title}</p>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={pending.qrPng} alt="Xaman QR code" className="xaman-qr__img" />
      <p className="xaman-qr__waiting">Waiting for signature…</p>
      <a
        href={pending.deeplink}
        target="_blank"
        rel="noopener noreferrer"
        className="xaman-qr__link"
      >
        Open in Xaman app
      </a>
      <button type="button" className="secondary" onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}
