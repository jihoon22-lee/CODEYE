import React from "react";
import DOMPurify from "dompurify";

export function Banner() {
  const message = new URLSearchParams(window.location.search).get("message") ?? "";
  return <div dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(message) }} />;
}

export function Plain() {
  const message = new URLSearchParams(window.location.search).get("message") ?? "";
  return <div>{message}</div>;
}
